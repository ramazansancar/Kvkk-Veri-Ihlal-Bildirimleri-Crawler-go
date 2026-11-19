package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// Transport-level errors seen while fetching listing pages.
var fetchErrs []error

// kvkk.gov.tr serves only its leaf certificate and omits the GlobalSign
// intermediate that signed it. Browsers hide this by downloading the missing
// intermediate from the URL in the certificate's Authority Information Access
// (AIA) extension; Go does not, so every request failed with
// "x509: certificate signed by unknown authority". The transport below does the
// same AIA fetch the browsers do.

// kvkkTransport is shared by every collector so connections are reused.
var kvkkTransport = newKvkkTransport()

func newKvkkTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{
		// Go's built-in verification only uses the chain the server sends, so it
		// has to be turned off and replaced by VerifyConnection. This is not a
		// verification bypass: verifyWithAIA runs the full check (system roots,
		// expiry, hostname) and the handshake is aborted if it returns an error.
		InsecureSkipVerify: true,
		VerifyConnection:   verifyWithAIA,
	}
	return t
}

var (
	aiaMu     sync.Mutex
	aiaCache  = map[string]*x509.Certificate{}
	aiaClient = &http.Client{Timeout: 15 * time.Second}
)

// verifyWithAIA verifies the server chain like Go's default verifier. If that
// fails only because an intermediate is missing, it downloads the issuer from
// the AIA URL and retries. Downloaded certificates are used as intermediates
// only, never as trust anchors, so the chain must still end in a system root.
func verifyWithAIA(cs tls.ConnectionState) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("tls: server presented no certificates")
	}
	leaf := cs.PeerCertificates[0]
	intermediates := x509.NewCertPool()
	for _, cert := range cs.PeerCertificates[1:] {
		intermediates.AddCert(cert)
	}
	opts := x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: intermediates}

	_, err := leaf.Verify(opts)
	var unknownAuthority x509.UnknownAuthorityError
	if err == nil || !errors.As(err, &unknownAuthority) {
		return err
	}

	// Walk up the AIA links (bounded) until the chain reaches a trusted root.
	cert := leaf
	for depth := 0; depth < 3 && len(cert.IssuingCertificateURL) > 0; depth++ {
		issuer, fetchErr := fetchIssuer(cert.IssuingCertificateURL[0])
		if fetchErr != nil {
			return fmt.Errorf("%w (AIA fetch failed: %v)", err, fetchErr)
		}
		intermediates.AddCert(issuer)
		if _, err = leaf.Verify(opts); err == nil {
			return nil
		}
		cert = issuer
	}
	return err
}

// fetchIssuer downloads (and caches for the run) the certificate at an AIA
// "CA Issuers" URL. These are usually plain DER, occasionally PEM.
func fetchIssuer(u string) (*x509.Certificate, error) {
	aiaMu.Lock()
	defer aiaMu.Unlock()
	if cert, ok := aiaCache[u]; ok {
		return cert, nil
	}

	resp, err := aiaClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	cert, err := x509.ParseCertificate(body)
	if err != nil {
		if block, _ := pem.Decode(body); block != nil {
			cert, err = x509.ParseCertificate(block.Bytes)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("parse certificate from %s: %v", u, err)
	}
	aiaCache[u] = cert
	return cert, nil
}

func getArticleContent(noticeUrl string) string {
	var content strings.Builder
	c := colly.NewCollector(
		colly.AllowedDomains("kvkk.gov.tr", "www.kvkk.gov.tr"),
	)
	c.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	c.WithTransport(kvkkTransport)

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

// writeJSON writes data.json. Write to a temp file and rename so a failed or
// interrupted write can never leave a truncated data.json behind.
func writeJSON(fName string, notices []Notice) error {
	tmp := fName + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	// Keep "&", "<", ">" as-is instead of "\u0026" etc.: data.json is read by
	// people and tools, never embedded in HTML, so the escaping only hurts.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(notices); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, fName)
}

// writeCSV derives data.csv from the same records that go into data.json so the
// two files never drift apart. Written on every run, including runs that add no
// new notices, so a missing/stale CSV is repaired without waiting for new data.
func writeCSV(fName string, notices []Notice) error {
	file, err := os.Create(fName)
	if err != nil {
		return err
	}
	defer file.Close()

	// UTF-8 BOM so Excel opens the Turkish characters correctly.
	if _, err := file.WriteString("\ufeff"); err != nil {
		return err
	}

	w := csv.NewWriter(file)
	if err := w.Write([]string{"date", "title", "url", "image", "content"}); err != nil {
		return err
	}
	for _, n := range notices {
		if err := w.Write([]string{n.Date, n.Title, n.Url, n.Image, n.Content}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// visitWithRetry retries a listing page on transport-level failures (connection
// refused/reset, TLS handshake, timeout). The site is periodically unreachable from
// GitHub's Azure runners around the 04:00 UTC cron slot, which previously surfaced as
// the misleading "listing markup changed" error. Returns the last error if all
// attempts fail so the caller can tell "site unreachable" from "markup changed".
//
// newCollector must return a fresh collector each call: colly records a URL as
// visited before the request is sent, so retrying on the same collector returns
// ErrAlreadyVisited instead of actually re-fetching.
func visitWithRetry(newCollector func() *colly.Collector, pageUrl string, attempts int) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			// Exponential backoff with jitter: ~4s, ~8s, ~16s.
			backoff := time.Duration(1<<uint(i)) * 2 * time.Second
			backoff += time.Duration(rand.Intn(1000)) * time.Millisecond
			fmt.Printf("Retry %d/%d for %s in %s (last error: %v)\n", i, attempts-1, pageUrl, backoff.Round(time.Second), lastErr)
			time.Sleep(backoff)
		}
		if lastErr = newCollector().Visit(pageUrl); lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func main() {
	fName := "data.json"
	csvName := "data.csv"
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

	newNotices := []Notice{}
	// Counts every notice link seen on the listing pages (new or already known).
	// Zero means the scrape itself failed (bad URL, markup change, block page) rather
	// than "the site published nothing new" - those must not be confused.
	seenOnSite := 0
	maxPage := 1
	// Transport-level failures recorded by OnError, used below to tell a site
	// outage apart from a markup change when nothing was scraped.
	fetchErrs = nil

	// Each retry needs a fresh collector (colly marks a URL visited before the
	// request is sent), so build the fully-configured listing collector here.
	newListingCollector := func() *colly.Collector {
		lc := colly.NewCollector(
			colly.AllowedDomains("kvkk.gov.tr", "www.kvkk.gov.tr"),
		)
		lc.UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
		lc.WithTransport(kvkkTransport)
		lc.SetRequestTimeout(30 * time.Second)

		// Without an OnError handler colly swallows transport errors, so a site outage
		// looked identical to a markup change in the logs. Record them instead.
		lc.OnError(func(r *colly.Response, err error) {
			status := 0
			if r != nil {
				status = r.StatusCode
			}
			fmt.Printf("Request failed [status %d] %s: %v\n", status, r.Request.URL.String(), err)
			fetchErrs = append(fetchErrs, err)
		})

		lc.OnResponse(func(r *colly.Response) {
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
					lc.SetCookies(r.Request.URL.String(), []*http.Cookie{cookie})
					r.Request.Retry()
				}
			}
		})

		lc.OnHTML("ul.pagination", func(e *colly.HTMLElement) {
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

		lc.OnHTML("div.news__box", func(e *colly.HTMLElement) {
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
		return lc
	}

	fmt.Println("Starting crawler...")
	// Start with Page 1
	if err := visitWithRetry(newListingCollector, apiUrl+"1", 4); err != nil {
		log.Fatalf("Cannot fetch listing page 1 after retries: %s\n", err)
	}

	// If maxPage was updated, visit other pages
	for i := 2; i <= maxPage; i++ {
		fmt.Printf("Visiting Page %d...\n", i)
		time.Sleep(2 * time.Second)
		if err := visitWithRetry(newListingCollector, apiUrl+strconv.Itoa(i), 4); err != nil {
			// Page 1 already succeeded, so this is a partial fetch. Bail out rather
			// than treating a truncated crawl as a complete one.
			log.Fatalf("Cannot fetch listing page %d after retries: %s\n", i, err)
		}
	}

	// A run that saw no notice links at all did not "find nothing new" - it failed to
	// scrape. Exiting 0 here is what let the workflow stay green for 7 months while
	// data.json went stale, so make it a hard error.
	if seenOnSite == 0 {
		if len(fetchErrs) > 0 {
			log.Fatalf("Scrape failed: listing pages could not be fetched (%d transport error(s), last: %v). "+
				"The site was likely unreachable - this is not a markup change.\n", len(fetchErrs), fetchErrs[len(fetchErrs)-1])
		}
		log.Fatalln("Scrape failed: listing pages loaded but contained no notices. " +
			"The listing URL or page markup likely changed - refusing to report success.")
	}

	fmt.Printf("Scraped %d notices from %d page(s).\n", seenOnSite, maxPage)

	finalNotices := existingNotices
	if len(newNotices) > 0 {
		finalNotices = append(newNotices, existingNotices...)
		fmt.Printf("New notices added: %d. Total records: %d\n", len(newNotices), len(finalNotices))
	} else {
		fmt.Println("No new notices found to add.")
	}
	// Safety net: never let a run shrink the dataset (e.g. due to a scraping
	// regression), which is what caused the historical data loss incident.
	if len(finalNotices) < len(existingNotices) {
		log.Fatalf("Refusing to write: final record count (%d) is lower than existing (%d)\n", len(finalNotices), len(existingNotices))
	}

	// Rewrite data.json on every run, not only when something new was found, so a
	// format change (like the escaping fix in writeJSON) also normalizes existing records.
	// The output is deterministic, so an unchanged dataset produces an identical
	// file and the workflow has nothing to commit.
	if err := writeJSON(fName, finalNotices); err != nil {
		log.Fatalf("Cannot write file %q: %s\n", fName, err)
	}
	fmt.Printf("Wrote %s with %d records.\n", fName, len(finalNotices))

	// data.csv is a derived view of data.json, so regenerate it on every run - even
	// when nothing new was added - so the two files never drift apart.
	if err := writeCSV(csvName, finalNotices); err != nil {
		log.Fatalf("Cannot write file %q: %s\n", csvName, err)
	}
	fmt.Printf("Wrote %s with %d records.\n", csvName, len(finalNotices))
}
