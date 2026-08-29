package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
)

type Notice struct {
	Date    string `json:"date"`
	Title   string `json:"title"`
	Url     string `json:"url"`
	Image   string `json:"image"`
	Content string `json:"content"`
}

var (
	baseUrl = "https://www.kvkk.gov.tr"
	// NOTE: no trailing slash before "?" - "/veri-ihlali-bildirimi/?page=N" returns a
	// 301 to the slashless form. colly does not follow that redirect for the listing
	// pages, so the crawler silently scraped nothing (cause of the 7-month data gap).
	apiUrl = baseUrl + "/veri-ihlali-bildirimi?page="
)

// Global map to track processed URLs throughout the session
var processedInSession = make(map[string]bool)

func getArticleContent(noticeUrl string) string {
	var content strings.Builder
	c := colly.NewCollector(
		colly.AllowedDomains("kvkk.gov.tr", "www.kvkk.gov.tr"),
	)
	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	c.OnResponse(func(r *colly.Response) {
		if strings.Contains(string(r.Body), "resend()") {
			re := regexp.MustCompile(`sto-idd=([^;"]+)`)
			match := re.FindStringSubmatch(string(r.Body))
			if len(match) > 1 {
				cookie := &http.Cookie{
					Name:   "sto-idd",
					Value:  match[1],
					Domain: "www.kvkk.gov.tr",
					Path:   "/",
				}
				c.SetCookies(r.Request.URL.String(), []*http.Cookie{cookie})
				r.Request.Retry()
			}
		}
	})

	c.OnHTML("div.news__detail-article", func(e *colly.HTMLElement) {
		e.ForEach("p, ul", func(_ int, el *colly.HTMLElement) {
			if el.Name == "p" {
				text := strings.TrimSpace(el.Text)
				if text != "" {
					content.WriteString(text + "\n\n")
				}
			} else if el.Name == "ul" {
				el.ForEach("li", func(_ int, li *colly.HTMLElement) {
					content.WriteString("- " + strings.TrimSpace(li.Text) + "\n")
				})
				content.WriteString("\n")
			}
		})
	})

	c.Visit(noticeUrl)
	return strings.TrimSpace(content.String())
}

// normalizeDate strips the weekday suffix the listing started appending
// ("26 Agustos 2026, Carsamba" -> "26 Agustos 2026") so new records keep the
// same "<gun> <ay> <yil>" shape as every record already in data.json.
func normalizeDate(raw string) string {
	d := strings.TrimSpace(raw)
	if idx := strings.Index(d, ","); idx != -1 {
		d = d[:idx]
	}
	return strings.TrimSpace(d)
}

func main() {
	fName := "data.json"
	existingNotices := []Notice{}
	absPath, _ := filepath.Abs(fName)
	if _, err := os.Stat(absPath); err == nil {
		fileData, err := os.ReadFile(absPath)
		if err != nil {
			log.Fatalf("Cannot read existing file %q: %s\n", fName, err)
		}
		// Fail fast instead of silently treating a corrupt/unreadable file as "no data",
		// which would cause the merge below to overwrite existing records with an empty list.
		if len(strings.TrimSpace(string(fileData))) > 0 {
			if err := json.Unmarshal(fileData, &existingNotices); err != nil {
				log.Fatalf("Cannot parse existing file %q: %s\n", fName, err)
			}
		}
	}

	normalizeURL := func(u string) string {
		parsed, _ := url.Parse(u)
		host := strings.TrimPrefix(parsed.Host, "www.")
		path := strings.TrimSuffix(parsed.Path, "/")
		// Handle Turkish characters in URL if any
		return host + path
	}

	noticeMap := make(map[string]bool)
	for _, n := range existingNotices {
		noticeMap[normalizeURL(n.Url)] = true
	}

	c := colly.NewCollector(
		colly.AllowedDomains("kvkk.gov.tr", "www.kvkk.gov.tr"),
	)
	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	c.OnResponse(func(r *colly.Response) {
		fmt.Println("DEBUG status:", r.StatusCode, "len:", len(r.Body), "url:", r.Request.URL.String())
		if strings.Contains(string(r.Body), "resend()") {
			fmt.Println("Security challenge detected. Retrying with session cookie...")
			re := regexp.MustCompile(`sto-idd=([^;"]+)`)
			match := re.FindStringSubmatch(string(r.Body))
			if len(match) > 1 {
				cookie := &http.Cookie{
					Name:   "sto-idd",
					Value:  match[1],
					Domain: "www.kvkk.gov.tr",
					Path:   "/",
				}
				c.SetCookies(r.Request.URL.String(), []*http.Cookie{cookie})
				r.Request.Retry()
			}
		}
	})

	newNotices := []Notice{}
	// Counts every notice link seen on the listing pages (new or already known).
	// Zero means the scrape itself failed (bad URL, markup change, block page) rather
	// than "the site published nothing new" - those must not be confused.
	seenOnSite := 0
	maxPage := 1

	c.OnHTML("ul.pagination", func(e *colly.HTMLElement) {
		// Rely on the numeric "page=" value rather than link text (site copy like
		// "Son"/"Son Sayfa"/"»" changes over time), so pick the highest page number found.
		e.ForEach("li a[href]", func(_ int, el *colly.HTMLElement) {
			href := el.Attr("href")
			if strings.Contains(href, "page=") {
				parts := strings.Split(href, "page=")
				val := parts[len(parts)-1]
				if idx := strings.IndexAny(val, "&#"); idx != -1 {
					val = val[:idx]
				}
				if p, err := strconv.Atoi(val); err == nil && p > maxPage {
					maxPage = p
				}
			}
		})
	})

	c.OnHTML("div.news__box", func(e *colly.HTMLElement) {
		u := e.ChildAttr("div.news__box-meta > a", "href")
		if u == "" {
			return
		}

		if !strings.HasPrefix(u, "http") {
			if strings.HasPrefix(u, "/") {
				u = baseUrl + u
			} else {
				u = baseUrl + "/" + u
			}
		}

		seenOnSite++

		normUrl := normalizeURL(u)
		if _, exists := noticeMap[normUrl]; !exists {
			if _, processed := processedInSession[normUrl]; !processed {
				fmt.Printf("New notice found: [%s] %s\n", e.ChildText("p.date"), u)

				imgUrl := e.ChildAttr("img", "src")
				if imgUrl != "" && !strings.HasPrefix(imgUrl, "http") {
					imgUrl = baseUrl + imgUrl
				}

				notice := Notice{
					Date:    normalizeDate(e.ChildText("div.news__box-meta > p.date")),
					Title:   strings.TrimSpace(e.ChildText("div.news__box-meta > a")),
					Url:     u,
					Image:   imgUrl,
					Content: getArticleContent(u),
				}
				newNotices = append(newNotices, notice)
				processedInSession[normUrl] = true
			}
		}
	})

	fmt.Println("Starting crawler...")
	// Start with Page 1
	c.Visit(apiUrl + "1")

	// If maxPage was updated, visit other pages
	for i := 2; i <= maxPage; i++ {
		fmt.Printf("Visiting Page %d...\n", i)
		time.Sleep(2 * time.Second)
		c.Visit(apiUrl + strconv.Itoa(i))
	}

	// A run that saw no notice links at all did not "find nothing new" - it failed to
	// scrape. Exiting 0 here is what let the workflow stay green for 7 months while
	// data.json went stale, so make it a hard error.
	if seenOnSite == 0 {
		log.Fatalln("Scrape failed: no notices found on any listing page. " +
			"The listing URL or page markup likely changed - refusing to report success.")
	}

	fmt.Printf("Scraped %d notices from %d page(s).\n", seenOnSite, maxPage)

	if len(newNotices) > 0 {
		finalNotices := append(newNotices, existingNotices...)
		// Safety net: never let a run shrink the dataset (e.g. due to a scraping
		// regression), which is what caused the historical data loss incident.
		if len(finalNotices) < len(existingNotices) {
			log.Fatalf("Refusing to write: final record count (%d) is lower than existing (%d)\n", len(finalNotices), len(existingNotices))
		}
		file, err := os.Create(fName)
		if err != nil {
			log.Fatalf("Cannot create file %q: %s\n", fName, err)
			return
		}
		defer file.Close()

		enc := json.NewEncoder(file)
		enc.SetIndent("", "  ")
		enc.Encode(finalNotices)
		fmt.Printf("Crawler finished. New notices added: %d. Total records: %d\n", len(newNotices), len(finalNotices))
	} else {
		fmt.Println("No new notices found to add.")
	}
}
