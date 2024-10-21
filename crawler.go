package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
)

type Notice struct {
	Date    string `json:"date"`
	Title   string `json:"title"`
	Url     string `json:"url"`
	Image   string `json:"image"`
	Content string `json:"content"`
}

// Static and dynamic Variables
var (
	baseUrl = "https://kvkk.gov.tr"
	apiUrl  = baseUrl + "/veri-ihlali-bildirimi/?page="
	maxPage = 1
)

// Get article content
func getArticleContent(url string) string {
	type Article struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	article := Article{}
	c := colly.NewCollector(
		// Visit only domains: hackerspaces.org, wiki.hackerspaces.org
		colly.AllowedDomains("kvkk.gov.tr"),

		// Cache responses to prevent multiple download of pages
		// even if the collector is restarted
		colly.CacheDir("./kvkk_cache"),
	)

	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting Detail: ", r.URL.String())
	})

	c.OnHTML("div.blog-post-inner", func(e *colly.HTMLElement) {
		article.Title = e.ChildText("h3.blog-post-title")
		article.Content = e.ChildText("div.blog-post-inner > div")
	})

	c.Visit(url)

	return article.Content
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

	/*
		<div class="row mt-5 pagination">
			<div class="col-md-12">
				<nav>
					<ul class="pagination justify-content-center">
						<li class="page-item active"><a class="page-link">1</a></li>
						<li class="page-item"><a class="page-link" href="/veri-ihlali-bildirimi/?&amp;page=2">2</a></li>
						...
						<li class="page-item"><a class="page-link" href="/veri-ihlali-bildirimi/?&amp;page=10">10</a></li>
						<li class="page-item"><a class="page-link" href="/veri-ihlali-bildirimi/?&amp;page=2">Sonraki</a></li>
						<li class="page-item"><a class="page-link" href="/veri-ihlali-bildirimi/?&amp;page=26">Son</a></li>
					</ul>
				</nav>
			</div>
		</div>
	*/
	// Max Page fetcher
	c.OnHTML("ul.pagination", func(e *colly.HTMLElement) {
		e.ForEach("li", func(_ int, el *colly.HTMLElement) {
			// Fetch "Son" a href value split "page=" and get the number
			if el.ChildText("a") == "Son" {
				maxPage, _ = strconv.Atoi(el.ChildAttr("a", "href")[strings.Index(el.ChildAttr("a", "href"), "page=")+5:])
			}
		})
	})

	/*
		<div class="blog-post-container">
			<div class="blog-post-image">
				<img src="/SharedFolderServer/ContentImages/d74ed9d6-67d1-45a1-8c3e-349834c0bb73.jpg" alt="">
				<div class="blog-post-meta"></div>
			</div>
			<div class="blog-post-inner">
				<p class="small-text">17 Ekim 2024</p>
				<h3 class="blog-post-title">Kamuoyu Duyurusu (Veri İhlali Bildirimi) – Lokman Hekim &#220;niversitesi</h3>
				<p></p>

				<div class="row justify-content-end">
					<a target="_self" href="/Icerik/8041/Kamuoyu-Duyurusu-Veri-Ihlali-Bildirimi-Lokman-Hekim-Universitesi" class="arrow-link all-items"> Devamını G&#246;r </a>
				</div>
			</div>
		</div>
	*/
	// Blog post page scraper
	c.OnHTML("div.blog-post-container", func(e *colly.HTMLElement) {
		notice := Notice{
			Date:    e.ChildText("div.blog-post-inner > p.small-text"),
			Title:   e.ChildText("h3.blog-post-title"),
			Url:     baseUrl + e.ChildAttr("a", "href"),
			Image:   baseUrl + e.ChildAttr("div.blog-post-image > img", "src"),
			Content: getArticleContent(baseUrl + e.ChildAttr("a", "href")),
			//Title:       title,
			//URL:         e.Request.URL.String(),
			//Description: e.ChildText("div.content"),
			//Creator:     e.ChildText("li.banner-instructor-info > a > div > div > span"),
			//Rating:      e.ChildText("span.number-rating"),
		}

		// fmt.Printf("Found: %q -> %s \nImage: %s\nContent: %s", notice.title, notice.url, notice.image, notice.content)

		notices = append(notices, notice)
		//fmt.Println(notices)
	})

	/*
		<div class="blog-grid-item h-100 d-block">
			<div class="blog-grid-thumb">
				<a target="_self" href="/Icerik/8035/Kamuoyu-Duyurusu-Veri-Ihlali-Bildirimi-Kilis-7-Aralik-Universitesi">
					<img src="/Image/CropImage?w=420&amp;h=205&amp;f=/SharedFolderServer/ContentImages/b7f03eba-7adc-44a7-aea7-fda7b400b841.jpg" alt="">
				</a>
			</div>
			<div class="box-content-inner">
				<h4 class="blog-grid-title"><a target="_self" data-placement="bottom" data-toggle="tooltip" title="Kamuoyu Duyurusu (Veri İhlali Bildirimi) – Kilis 7 Aralık &#220;niversitesi" href="/Icerik/8035/Kamuoyu-Duyurusu-Veri-Ihlali-Bildirimi-Kilis-7-Aralik-Universitesi">Kamuoyu Duyurusu (Veri İhlali Bildirimi) – Kilis 7 Aralık &#220;niversitesi...</a></h4>
				<p class="blog-grid-meta small-text"><span><a target="_self" href="/Icerik/8035/Kamuoyu-Duyurusu-Veri-Ihlali-Bildirimi-Kilis-7-Aralik-Universitesi">09 Ekim 2024</a></span> </p>
			</div>
		</div>
	*/
	// Blog grid page scraper
	c.OnHTML("div.blog-grid-item", func(e *colly.HTMLElement) {
		notice := Notice{
			Date:    e.ChildText("p.blog-grid-meta > span > a"),
			Title:   e.ChildAttr("h4.blog-grid-title > a", "title"),
			Url:     baseUrl + e.ChildAttr("div.blog-grid-thumb > a", "href"),
			Image:   strings.Replace(baseUrl+e.ChildAttr("div.blog-grid-thumb > a > img", "src"), "/Image/CropImage?w=420&h=205&f=", "", 1),
			Content: getArticleContent(baseUrl + e.ChildAttr("div.blog-grid-thumb > a", "href")),
		}
		notices = append(notices, notice)
	})

	// Before making a request print "Visiting ..."
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting: ", r.URL.String())
	})

	for i := 1; i <= maxPage; i++ {
		// Url: https://kvkk.gov.tr/veri-ihlali-bildirimi/?page=1
		c.Visit(apiUrl + strconv.Itoa(i))
	}

	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")

	// Dump json to the standard output
	enc.Encode(notices)

}

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

// On every a element which has href attribute call callback
/*c.OnHTML("a[href]", func(e *colly.HTMLElement) {
link := e.Attr("href")
// Print link
fmt.Printf("Link found: %q -> %s\n", e.Text, link)
// Visit link found on page
// Only those links are visited which are in AllowedDomains
c.Visit(e.Request.AbsoluteURL(link))
})*/
