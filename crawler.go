package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/gocolly/colly/v2"
)

/*func main() {
    fmt.Println("Hello, World!")
}*/

/*func (n Notice) String() string {
	return fmt.Sprintf("%s %s %s %s %s", n.date, n.title, n.url, n.image, n.content)
}

func (n Notice) toJSON() string {
	b, err := json.Marshal(n)
	if err != nil {
		fmt.Println(err)
		return ""
	}
	return string(b)
}*/

type Notice struct {
	date    string `json:"date"`
	title   string `json:"title"`
	url     string `json:"url"`
	image   string `json:"image"`
	content string `json:"content"`
}

// Static and dynamic Variables
var (
	baseUrl = "https://kvkk.gov.tr"
	apiUrl  = baseUrl + "/veri-ihlali-bildirimi/?page="
)

func getArticleContent(url string) string {
	c := colly.NewCollector()
	c.OnHTML("div.blog-post-inner", func(e *colly.HTMLElement) {
		fmt.Println(e.ChildText("div"))
		e.ChildText("div")
	})
	return ""
}

func main() {
	fName := "data.json"
	file, err := os.Create(fName)
	if err != nil {
		log.Fatalf("Cannot create file %q: %s\n", fName, err)
		return
	}
	defer file.Close()

	// Instantiate default collector
	c := colly.NewCollector(
		// Visit only domains: hackerspaces.org, wiki.hackerspaces.org
		colly.AllowedDomains("kvkk.gov.tr"),

		// Cache responses to prevent multiple download of pages
		// even if the collector is restarted
		colly.CacheDir("./kvkk_cache"),
	)

	// Create another collector to scrape additional details
	// detailCollector := c.Clone()

	notices := make([]Notice, 0, 200)

	// Blog post page scraper
	c.OnHTML("div.blog-post-container", func(e *colly.HTMLElement) {

		notice := Notice{
			date:    e.ChildText("div.blog-post-inner > div.small-text"),
			title:   e.ChildText("h3.blog-post-title"),
			url:     e.ChildAttr("a", "href"),
			image:   e.ChildAttr("div.blog-post-image > img", "src"),
			content: getArticleContent(e.ChildAttr("a", "href")),
			//Title:       title,
			//URL:         e.Request.URL.String(),
			//Description: e.ChildText("div.content"),
			//Creator:     e.ChildText("li.banner-instructor-info > a > div > div > span"),
			//Rating:      e.ChildText("span.number-rating"),
		}

		// fmt.Printf("Found: %q -> %s \nImage: %s\nContent: %s", notice.title, notice.url, notice.image, notice.content)

		notices = append(notices, notice)
		fmt.Println(notices)

		/*if e.Attr("class") == "Button_1qxkboh-o_O-primary_cv02ee-o_O-md_28awn8-o_O-primaryLink_109aggg" {
			return
		}
		link := e.Attr("href")
		// If link start with browse or includes either signup or login return from callback
		if !strings.HasPrefix(link, "/browse") || strings.Index(link, "=signup") > -1 || strings.Index(link, "=login") > -1 {
			return
		}
		// start scaping the page under the link found
		e.Request.Visit(link)*/
	})
	// On every a element which has href attribute call callback
	/*c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		// Print link
		fmt.Printf("Link found: %q -> %s\n", e.Text, link)
		// Visit link found on page
		// Only those links are visited which are in AllowedDomains
		c.Visit(e.Request.AbsoluteURL(link))
	})*/

	// Before making a request print "Visiting ..."
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting", r.URL.String())
	})

	for i := 1; i <= 18; i++ {
		c.Visit(apiUrl + strconv.Itoa(i))
	}
	// Start scraping on kvkk.gov.tr
	//c.Visit(apiUrl+)

	//c.Visit("https://kvkk.gov.tr/veri-ihlali-bildirimi/?page=1")

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	// Dump json to the standard output
	enc.Encode(notices)

}
