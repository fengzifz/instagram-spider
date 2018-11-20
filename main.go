package main

import (
	"bufio"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"github.com/gocolly/colly"
	"log"
	"math/rand"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// "id": user id, "after": end cursor
const nextPageURL string = `https://www.instagram.com/graphql/query/?query_hash=%s&variables=%s`
const nextPagePayload string = `{"id":"%s","first":50,"after":"%s"}`
const insImageDir = "./instagram"

var requestID string
var requestIds [][]byte
var queryIdPattern = regexp.MustCompile(`queryId:".{32}"`)

type pageInfo struct {
	EndCursor string `json:"end_cursor"`
	NextPage  bool   `json:"has_next_page"`
}

// 当前页的信息
type mainPageData struct {
	Rhxgis    string `json:"rhx_gis"`
	EntryData struct {
		ProfilePage []struct {
			Graphql struct {
				User struct {
					Id    string `json:"id"`
					Media struct {
						Edges []struct {
							Node struct {
								ImageURL     string `json:"display_url"`
								ThumbnailURL string `json:"thumbnail_src"`
								IsVideo      bool   `json:"is_video"`
								Date         int    `json:"date"`
								Dimensions   struct {
									Width  int `json:"width"`
									Height int `json:"height"`
								} `json:"dimensions"`
							} `json::node"`
						} `json:"edges"`
						PageInfo pageInfo `json:"page_info"`
					} `json:"edge_owner_to_timeline_media"`
				} `json:"user"`
			} `json:"graphql"`
		} `json:"ProfilePage"`
	} `json:"entry_data"`
}

// 下一页的信息
type nextPageData struct {
	Data struct {
		User struct {
			Container struct {
				PageInfo pageInfo `json:"page_info"`
				Edges    []struct {
					Node struct {
						ImageURL     string `json:"display_url"`
						ThumbnailURL string `json:"thumbnail_src"`
						IsVideo      bool   `json:"is_video"`
						Date         int    `json:"taken_at_timestamp"`
						Dimensions   struct {
							Width  int `json:"width"`
							Height int `json:"height"`
						}
					}
				} `json:"edges"`
			} `json:"edge_owner_to_timeline_media"`
		}
	} `json:"data"`
}

func instagramSpider(instagramAccount string) {
	var actualUserId string
	outputDir := fmt.Sprintf(insImageDir+"/instagram_%s/", instagramAccount)

	// 设置 user agent
	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (Windows NT 6.1) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/41.0.2228.0 Safari/537.36"),
	)

	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("X-Requested-With", "XMLHttpRequest")
		r.Headers.Set("Referrer", "https://www.instagram.com/"+instagramAccount)

		// X-Instagram-GIS 是 Instagram 后来设置的反爬虫头部信息
		// 在初始请求中，会找到一个包含了 gis 字符的变量，rhx_gis，它是一个 md5 序列，用于跟服务器进行验证
		// 但这个机制在 2018 年 4 月已经去掉了
		if r.Ctx.Get("gis") != "" {
			gis := fmt.Sprintf("%s:%s", r.Ctx.Get("gis"), r.Ctx.Get("variables"))
			h := md5.New()
			h.Write([]byte(gis))
			gisHash := fmt.Sprintf("%x", h.Sum(nil))
			r.Headers.Set("X-Instagram-GIS", gisHash)
		}
	})

	c.OnHTML("html", func(e *colly.HTMLElement) {
		d := c.Clone()
		d.OnResponse(func(r *colly.Response) {
			requestIds = queryIdPattern.FindAll(r.Body, -1)
			requestID = string(requestIds[1][9:41])
		})
		requestIDURL := e.Request.AbsoluteURL(e.ChildAttr(`link[as="script"]`, "href"))
		d.Visit(requestIDURL)

		dat := e.ChildText("body > script:first-of-type")
		jsonData := dat[strings.Index(dat, "{") : len(dat)-1]
		data := &mainPageData{}
		err := json.Unmarshal([]byte(jsonData), data)
		if err != nil {
			log.Fatal(err)
		}

		log.Println("saving output to ", outputDir)
		os.MkdirAll(outputDir, os.ModePerm)
		page := data.EntryData.ProfilePage[0]
		actualUserId = page.Graphql.User.Id

		// 查找当前页面，用户有多少个 post
		for _, obj := range page.Graphql.User.Media.Edges {
			// 跳过 video
			if obj.Node.IsVideo {
				continue
			}
			c.Visit(obj.Node.ImageURL)
		}

		nextPageVars := fmt.Sprintf(nextPagePayload, actualUserId, page.Graphql.User.Media.PageInfo.EndCursor)
		e.Request.Ctx.Put("variables", nextPageVars)
		if page.Graphql.User.Media.PageInfo.NextPage {
			u := fmt.Sprintf(
				nextPageURL,
				requestID,
				url.QueryEscape(nextPageVars),
			)
			log.Println("Next page found", u)
			e.Request.Ctx.Put("gis", data.Rhxgis)
			e.Request.Visit(u)
		}
	})

	// 错误
	c.OnError(func(r *colly.Response, e error) {
		log.Println("error:", e, r.Request.URL, string(r.Body))
	})

	// 响应
	c.OnResponse(func(r *colly.Response) {

		// 保存图片
		if strings.Index(r.Headers.Get("Content-Type"), "image") > -1 {

			filename := outputDir + r.FileName()
			// 重复的不保存
			if _, err := os.Stat(filename); os.IsNotExist(err) {
				r.Save(outputDir + r.FileName())

				// 创建日志
				logFilePath := "./log/"
				if _, err := os.Stat(logFilePath); os.IsNotExist(err) {
					os.MkdirAll(logFilePath, os.ModePerm)
				}

				logFile := logFilePath + time.Now().Format("2006-01-02")
				if _, err := os.Stat(logFile); os.IsNotExist(err) {
					os.Create(logFile)
				}

				// 记录日志
				f, _ := os.OpenFile(logFile, os.O_WRONLY|os.O_APPEND, 0666)
				defer f.Close()
				f.WriteString(
					time.Now().Format("2006-01-02 15:04:05") +
						" [" + instagramAccount + "]: Save a new image.\n",
				)
			}

			return
		}

		// json 内容
		if strings.Index(r.Headers.Get("Content-Type"), "json") == -1 {
			return
		}

		data := &nextPageData{}
		err := json.Unmarshal(r.Body, data)
		if err != nil {
			log.Fatal(err)
		}

		for _, obj := range data.Data.User.Container.Edges {
			// 跳过 videos
			if obj.Node.IsVideo {
				continue
			}
			c.Visit(obj.Node.ImageURL)
		}

		// 下一页的内容，这里没有爬取
		if data.Data.User.Container.PageInfo.NextPage {
			nextPageVars := fmt.Sprintf(nextPagePayload, actualUserId, data.Data.User.Container.PageInfo.EndCursor)
			r.Request.Ctx.Put("variables", nextPageVars)
			u := fmt.Sprintf(
				nextPageURL,
				requestID,
				url.QueryEscape(nextPageVars),
			)
			log.Println("Next page found", u)
			r.Request.Visit(u)
		}
	})

	c.Visit("https://instagram.com/" + instagramAccount)
}

func init() {
	if _, err := os.Stat(insImageDir); os.IsNotExist(err) {
		os.MkdirAll(insImageDir, os.ModePerm)
	}
}

func readLines(path string) ([]string ,error) {
	file, err := os.Open(path)
	if err != nil {
		fmt.Println(err)
		os.Exit(-3)
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

func main() {
	accountSlice, _ := readLines("./accounts")

	// 循环爬取
	for _, value := range accountSlice {
		instagramSpider(value)
		// 间隔 10 - 60 秒才爬取下一个账户
		time.Sleep(time.Duration(rand.Intn(50)+10) * time.Second)
	}

}
