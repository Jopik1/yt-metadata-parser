/*
 * yt-metadata-parser by Jopik
 * Extending Youtube-MA https://github.com/CorentinB/YouTube-MA
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, version 3.
 *
 * This program is distributed in the hope that it will be useful, but
 * WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU
 * General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"

	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/gommon/color"
	"github.com/spf13/cast"
	"golang.org/x/net/html"
	"golang.org/x/net/proxy"
)
import crytporand "crypto/rand"

/*var roundTripper = &http.Transport{
	Dial: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).Dial,
	TLSHandshakeTimeout: 10 * time.Second,
	MaxIdleConns:        50,
	MaxIdleConnsPerHost: 20,
	IdleConnTimeout:     90 * time.Second,
}*/

var logger = log.New(os.Stdout, "", 0)

var formatVersion = 4
var isDebug = false
var outTransports = []*http.Transport{}

var cmdFlags = struct {
	DeepDirFormat        bool
	GetAutoSub           bool
	GetDescription       bool
	GetAnnotation        bool
	GetThumbnail         bool
	DualFormatExistCheck bool
	RemoveRedundantData  bool
	UseSockProxy         bool
	SubPathLen           int
	MasterServer         string
	Concurrency          int64
	WorkerUUID           string
}{
	true,  //DeepDirFormat
	false, //GetAutoSub
	true,  //GetDescription
	false, //GetAnnotation
	true,  //GetThumbnail
	false, //DualFormatExistCheck
	true,  // RemoveRedundantData
	false, //UseSockProxy
	2,
	"",
	8,
	"",
}

var combinedOutFile = struct {
	mutex   sync.Mutex // this mutex protects the file
	outFile io.Writer
	recID   int64
}{
	outFile: nil,
	recID:   0,
}

// Video structure containing all metadata for the video
type Video struct {
	ID                string
	Title             string
	Annotations       string
	Thumbnail         string
	Description       string
	Path              string
	RawHTML           string
	InfoJSON          infoJSON
	playerArgs        map[string]interface{}
	playerResponse    map[string]interface{}
	InteractionCount  int64
	GotPlayerResponse bool
	RawFormats        []url.Values
	HTTPTransport     *http.Transport
}

// Tracklist structure containing all subtitles tracks for the video
type Tracklist struct {
	Tracks []Track `xml:"track"`
}

// Track structure for data about single subtitle
type Track struct {
	LangCode    string `xml:"lang_code,attr"`
	Lang        string `xml:"lang_translated,attr"`
	LangDefault string `xml:"lang_default,attr"`
	Kind        string
	DirectURL   string
}

// Credit structure for data about a single credit given
type Credit struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	URL    string `json:"url"`
}

// RecommendedVideo structure for data about a single recommended video
type RecommendedVideo struct {
	VideoID     string `json:"video_id"`
	ViewCount   int64  `json:"view_count"`
	Title       string `json:"title,omitempty"`
	Attribution string `json:"attribution,omitempty"`
}

// HeadlineBadge structure for data about a single caption author
type HeadlineBadge struct {
	Text string `json:"text"`
	URL  string `json:"url"`
}

// infoJSON structure containing the generated json data
type infoJSON struct {
	Version           int                   `json:"v"`
	ID                string                `json:"id"`
	FetchedDate       string                `json:"fetch_date,omitempty"`
	Uploader          string                `json:"uploader,omitempty"`
	UploaderID        string                `json:"uploader_id,omitempty"`
	UploaderURL       string                `json:"uploader_url,omitempty"`
	UploadDate        string                `json:"upload_date,omitempty"`
	License           string                `json:"license,omitempty"`
	Creator           string                `json:"creator,omitempty"`
	Title             string                `json:"title,omitempty"`
	AltTitle          string                `json:"alt_title,omitempty"`
	Thumbnail         string                `json:"thumbnail,omitempty"`
	Description       string                `json:"description,omitempty"`
	Category          string                `json:"category,omitempty"`
	Tags              []string              `json:"tags,omitempty"`
	Subtitles         map[string][]Subtitle `json:"subtitles,omitempty"`
	Duration          int64                 `json:"duration"`
	AgeLimit          float64               `json:"age_limit"`
	Annotations       string                `json:"annotations,omitempty"`
	WebpageURL        string                `json:"webpage_url,omitempty"`
	ViewCount         int64                 `json:"view_count"`
	LikeCount         int64                 `json:"like_count"`
	DislikeCount      int64                 `json:"dislike_count"`
	AverageRating     float64               `json:"average_rating"`
	AllowEmbed        bool                  `json:"allow_embed"`
	IsCrawlable       bool                  `json:"is_crawlable"`
	AllowSubContrib   bool                  `json:"allow_sub_contrib"`
	IsLiveContent     bool                  `json:"is_live_content"`
	IsAdsEnabled      bool                  `json:"is_ads_enabled"`
	IsCommentsEnabled bool                  `json:"is_comments_enabled"`
	//TODO fill AllowSubContrib, IsLiveContent, LocationTag,LocationHash, resolutions
	Formats            []Format           `json:"formats,omitempty"`
	Credits            []Credit           `json:"credits,omitempty"`
	HeadlineBadges     []HeadlineBadge    `json:"headline_badges,omitempty"`
	RegionsAllowed     []string           `json:"regions_allowed,omitempty"`
	UnavailableMessage string             `json:"unavailable_message,omitempty"`
	RecommendedVideos  []RecommendedVideo `json:"recommended_videos,omitempty"`
}

func (e *infoJSON) Init() {
	e.Version = formatVersion
	e.AllowEmbed = false
	e.IsCrawlable = true
	e.AllowSubContrib = false
	e.IsLiveContent = false
	e.IsCommentsEnabled = true
}

//Subtitle structure
type Subtitle struct {
	URL       string `json:"url,omitempty"`
	Ext       string `json:"ext,omitempty"`
	IsDefault *bool  `json:"is_default,omitempty"`
}

// Format structure for all different formats informations
type Format struct {
	FormatID     string  `json:"format_id,omitempty"`
	Ext          string  `json:"ext,omitempty"`
	URL          string  `json:"url,omitempty"`
	Height       float64 `json:"height,omitempty"`
	Width        float64 `json:"width,omitempty"`
	FormatNote   string  `json:"format_note,omitempty"`
	Bitrate      float64 `json:"bitrate"`
	Fps          float64 `json:"fps,omitempty"`
	Format       string  `json:"format,omitempty"`
	Clen         float64 `json:"clen,omitempty"`
	EOTF         string  `json:"eotf,omitempty"`
	Index        string  `json:"index,omitempty"`
	Init         string  `json:"init,omitempty"`
	Lmt          float64 `json:"lmt,omitempty"`
	Primaries    string  `json:"primaries,omitempty"`
	QualityLabel string  `json:"quality_label,omitempty"`
	Type         string  `json:"type,omitempty"`
	Size         string  `json:"size,omitempty"`
}

// Format structure video quality labels
type QualityLabel struct {
	ID       int
	Label    string
	Priority int
}

type workBatch struct {
	BatchID   string   `json:"batch_id"`
	BatchUUID string   `json:"batch_uuid"`
	VideoIDS  []string `json:"videos_ids,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// newUUID generates a random UUID according to RFC 4122
func newUUID() (string, error) {
	uuid := make([]byte, 16)
	n, err := io.ReadFull(crytporand.Reader, uuid)
	if n != len(uuid) || err != nil {
		return "", err
	}
	// variant bits; see section 4.1.1
	uuid[8] = uuid[8]&^0xc0 | 0x80
	// version 4 (pseudo-random); see section 4.1.3
	uuid[6] = uuid[6]&^0xf0 | 0x40
	return fmt.Sprintf("%x-%x-%x-%x-%x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]), nil
}

func sendMultipart(url string, params map[string]string, field string, data *bytes.Buffer) (*http.Response, error) {
	r, w := io.Pipe()
	mWriter := multipart.NewWriter(w)

	go func() {
		defer w.Close()
		defer mWriter.Close()

		for key, val := range params {
			_ = mWriter.WriteField(key, val)
		}

		fWriter, err := mWriter.CreateFormFile(field, "data")

		if err != nil {
			fmt.Fprintf(os.Stderr, "CreateFormFile failed: %v\n", err)
			return
		}

		/*file, err := os.Open(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Open file %s failed: %v\n", name, err)
			return
		}
		defer file.Close()*/
		bufferReader := bufio.NewReader(data)
		if _, err = io.Copy(fWriter, bufferReader); err != nil {
			fmt.Fprintf(os.Stderr, "io.Copy failed: %v\n", err)
			return
		}
	}()

	return http.Post(url, mWriter.FormDataContentType(), r)
}

var qualityLabels = map[string]QualityLabel{}

var regexpGetLicense = regexp.MustCompile(`<h4[^>]+class="title"[^>]*>\s*License\s*</h4>\s*<ul[^>]*>\s*<li>(.+?)</li`)
var regexpGetAgeLimit = regexp.MustCompile(`(?s)<h4[^>]*>\s*Notice\s*</h4>\s*<ul[^>]*>(.*?)</ul>`)
var regexpGetCategory = regexp.MustCompile(`(?s)<h4[^>]*>\s*Category\s*</h4>\s*<ul[^>]*>(.*?)</ul>`)
var regLeaveOnlyDigits = regexp.MustCompile("[^0-9]+")

func getRandTransport() *http.Transport {
	return outTransports[rand.Intn(len(outTransports))]
}

func random(min int64, max int64) int64 {
	return rand.Int63n(max-min) + min
}

func initTransports(numTransports int) {

	for i := 0; i < numTransports; i++ {
		var transport = &http.Transport{
			Dial: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 15 * time.Second,
			}).Dial,
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        30,
			MaxIdleConnsPerHost: 30,
			IdleConnTimeout:     25 * time.Second,
		}
		outTransports = append(outTransports, transport)
	}

	fmt.Printf("Initialized %d transports\n", len(outTransports))
}

func initProxyTransports(numSocksTransports int) {
	for i := 0; i < numSocksTransports; i++ {
		port := random(1080, 1081) //1084 - no benifit from spliting across multiple connections
		dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.FormatInt(port, 10), nil, proxy.Direct)
		if err != nil {
			fmt.Fprintln(os.Stderr, "can't connect to the proxy:", err)
			os.Exit(1)
		}

		var transport = &http.Transport{
			TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns:        30,
			MaxIdleConnsPerHost: 30,
			IdleConnTimeout:     25 * time.Second,
		}
		transport.Dial = dialer.Dial
		outTransports = append(outTransports, transport)
	}
	fmt.Printf("Initialized %d socks5 transports\n", numSocksTransports)
}

func removeVideoFiles(video *Video) error {
	if cmdFlags.DeepDirFormat {
		files, err := filepath.Glob(video.Path + video.ID + "*")
		if err != nil {
			return err
		}
		for _, f := range files {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	} else {
		os.RemoveAll(video.Path)
	}
	return nil
}

func fetchAnnotations(video *Video) {
	// requesting annotations from YouTube
	userAgent := ""
	resp, err := HTTPRequestCustomUserAgent("https://www.youtube.com/annotations_invideo?features=1&legacy=1&video_id="+video.ID, userAgent, video.HTTPTransport)

	//resp, err := http.Get()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
	defer resp.Body.Close()
	// checking response status code
	if resp.StatusCode == http.StatusOK {
		bodyBytes, err2 := ioutil.ReadAll(resp.Body)
		if err2 != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			runtime.Goexit()
		}
		annotations := string(bodyBytes)
		video.Annotations = annotations
		video.InfoJSON.Annotations = video.Annotations
	} else {
		//color.Println(color.Yellow("[") + color.Red("!") + color.Yellow("]") + color.Yellow("[") + color.Cyan(video.ID) + color.Yellow("]") + color.Red(" Unable to fetch annotations!"))
		logger.Println("[!][" + video.ID + "] Unable to fetch annotations!")
		video.Annotations = ""
		video.InfoJSON.Annotations = ""
	}
}

func writeFilesForRemoteUpload(video *Video) {
	JSON, _ := JSONMarshalIndentNoEscapeHTML(video.InfoJSON, "", " ")
	combinedOutFile.mutex.Lock()
	defer combinedOutFile.mutex.Unlock()
	if combinedOutFile.recID > 0 { //add delimiter if not first record
		combinedOutFile.outFile.Write([]byte(","))
	}
	combinedOutFile.recID++
	combinedOutFile.outFile.Write(JSON)
}

func writeFiles(video *Video) {
	// write annotations
	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	if cmdFlags.GetAnnotation {
		annotationsFile, errAnno := os.Create(baseFileName + ".annotations.xml")
		if errAnno != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", errAnno)
			runtime.Goexit()
		}
		fmt.Fprintf(annotationsFile, "%s", video.Annotations)
		defer annotationsFile.Close()
	}

	if cmdFlags.GetDescription {
		// write description
		descriptionFile, errDescription := os.Create(baseFileName + ".description")
		if errDescription != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", errDescription)
			runtime.Goexit()
		}
		fmt.Fprintf(descriptionFile, "%s", video.Description)
		defer descriptionFile.Close()
	}
	// write info json file
	infoFile, errInfo := os.Create(baseFileName + ".info.json")
	if errInfo != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", errInfo)
		runtime.Goexit()
	}
	defer infoFile.Close()

	JSON, _ := JSONMarshalIndentNoEscapeHTML(video.InfoJSON, "", " ")
	fmt.Fprintf(infoFile, "%s", string(JSON))
}

func downloadThumbnail(video *Video) {
	if len(video.Thumbnail) < 1 {
		removeVideoFiles(video)
		runtime.Goexit()
	}
	// create the file
	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	out, err := os.Create(baseFileName + ".jpg")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
	defer out.Close()
	// get the data
	resp, err := http.Get(video.Thumbnail)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		removeVideoFiles(video)
		runtime.Goexit()
	}
	defer resp.Body.Close()
	// write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
}

func parseDescription(video *Video, document *goquery.Document) {
	video.Description = ""
	// extract description
	desc := document.Find("#eow-description").Contents()
	desc.Each(func(i int, s *goquery.Selection) {
		switch s.Nodes[0].Type {
		case html.TextNode:
			video.Description += s.Text()
		case html.ElementNode:
			switch s.Nodes[0].Data {
			case "a":
				video.Description += s.Text()
			case "br":
				video.Description += "\n"
			default:
				fmt.Println("Unknown data type", s.Nodes[0].Data)
			}
		default:
			fmt.Println("Unknown node type", s.Nodes[0].Type)
		}
	})
}

func parseYTInitialData(video *Video, document *goquery.Document) {
	const pre = "window[\"ytInitialData\"] ="
	const post = "\n"

	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	// extract ytplayer.config
	script := document.Find("script")
	script.Each(func(i int, s *goquery.Selection) {
		js := s.Text()
		//fmt.Println(js)
		startI := strings.Index(js, pre)
		//fmt.Println(startI)
		if startI == -1 {
			return
		}

		endI := strings.Index(js[startI:], post)
		//fmt.Println(endI)
		if endI >= 0 {
			ytInitialData := js[startI+len(pre) : endI+startI]
			ytInitialData = strings.TrimRight(strings.TrimSpace(ytInitialData), ";")
			if isDebug {
				ioutil.WriteFile(baseFileName+".ytInitialData.json", []byte(ytInitialData), 0644)
			}
		}
	})
}

func parsePlayerArgs(video *Video, document *goquery.Document) {
	const pre = "var ytplayer = ytplayer || {};ytplayer.config = "
	const post = ";ytplayer.load "

	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	// extract ytplayer.config
	script := document.Find("div#player").Find("script")
	video.GotPlayerResponse = false
	script.Each(func(i int, s *goquery.Selection) {
		js := s.Text()
		if strings.HasPrefix(js, pre) {
			i := strings.Index(js, post)
			if i == -1 {
				return
			}
			strCfg := js[len(pre):i]
			var cfg struct {
				Args map[string]interface{}
			}
			//fmt.Println(strCfg)
			err := json.Unmarshal([]byte(strCfg), &cfg)
			if err != nil {
				fmt.Println(err)
				return
			}
			video.playerArgs = cfg.Args

			if isDebug {
				tmpData1 := new(bytes.Buffer)
				for key, value := range video.playerArgs {
					fmt.Fprintf(tmpData1, "%s=\"%s\"\n", key, value)
				}
				//tmpData1 := []byte((video.playerArgs).(string))

				ioutil.WriteFile(baseFileName+".playerArgs.json", tmpData1.Bytes(), 0644)

				tmpData := []byte((video.playerArgs["player_response"]).(string))
				ioutil.WriteFile(baseFileName+".player_response.json", tmpData, 0644)
			}

			if video.playerArgs["player_response"] == nil {
				fmt.Fprintf(os.Stderr, "Error: %s\n", "player_response not present in file")
				runtime.Goexit()
			}
			video.GotPlayerResponse = true
			err = json.Unmarshal([]byte((video.playerArgs["player_response"]).(string)), &video.playerResponse)
			if err != nil {
				//panic(err)
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				runtime.Goexit()
			}

		}
	})

	if !video.GotPlayerResponse {
		//fmt.Println("no player response, could be age restricted")
		userAgent := "" //"Go-http-client/1.1"
		html, err := HTTPRequestCustomUserAgent("https://www.youtube.com/get_video_info?video_id="+video.ID+"&ps=default&eurl=&gl=US&hl=en&disable_polymer=1", userAgent, video.HTTPTransport)
		body, err := ioutil.ReadAll(html.Body)
		valueMap, err := url.ParseQuery(string(body))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Unable to fetch get_video_info page %s %v\n", video.ID, err)
			return
		}

		if len(valueMap["player_response"]) > 0 {
			video.playerArgs["player_response"] = valueMap["player_response"][0]
			err = json.Unmarshal([]byte((video.playerArgs["player_response"]).(string)), &video.playerResponse)
			if err != nil {
				//panic(err)
				fmt.Fprintf(os.Stderr, "Error extracting player_response: %s %v\n", video.ID, err)
				return
			}
		}
		if len(valueMap["url_encoded_fmt_stream_map"]) > 0 {
			video.playerArgs["url_encoded_fmt_stream_map"] = valueMap["url_encoded_fmt_stream_map"][0]
		}
		if len(valueMap["adaptive_fmts"]) > 0 {
			video.playerArgs["adaptive_fmts"] = valueMap["adaptive_fmts"][0]
		}
		if len(valueMap["length_seconds"]) > 0 {
			video.playerArgs["length_seconds"] = valueMap["length_seconds"][0]
		}
	}
}

func parseIsCommentsEnabled(video *Video, document *goquery.Document) {
	document.Find("#watch-discussion").EachWithBreak(func(i int, s *goquery.Selection) bool {
		text := s.Text()
		if strings.Contains(text, "disabled") {
			video.InfoJSON.IsCommentsEnabled = false
		}
		return false
	})
}

func parseRecommendedVideos(video *Video, document *goquery.Document) {
	document.Find("div.content-wrapper").Each(func(i int, s *goquery.Selection) {
		newRecVideo := RecommendedVideo{}
		goodRec := false
		s.Find("a").EachWithBreak(func(j int, href *goquery.Selection) bool {
			URL, ok := href.Attr("href")
			if ok {
				u, err := url.Parse(URL)
				if err == nil {
					q := u.Query()
					if len(q.Get("v")) > 1 {
						newRecVideo.VideoID = q.Get("v")
						if !cmdFlags.RemoveRedundantData {
							newRecVideo.Title, _ = href.Attr("title")
						}
						goodRec = true
					}

				}

			}
			return false
		})
		/*
			<span class="stat attribution"><span class="" >Extra Credits</span></span>
			<span class="stat view-count">919,112 views</span>*/
		if !cmdFlags.RemoveRedundantData {
			s.Find("span.attribution").EachWithBreak(func(j int, s *goquery.Selection) bool {
				newRecVideo.Attribution = s.Text()
				return false
			})
		}

		s.Find("span.view-count").EachWithBreak(func(j int, s *goquery.Selection) bool {
			html, err := s.Html()
			//fmt.Println(html)
			viewsText := s.Text()
			if err == nil { //string extra html like 478,260 views<ul class="yt-badge-list "><li class="yt-badge-item"><span class="yt-badge ">360°</span></li></ul>
				split := strings.Split(html, "<")
				if len(split) > 0 {
					viewsText = split[0]
				}
			}
			//fmt.Println(viewsText)

			viewsTextStripped := regLeaveOnlyDigits.ReplaceAllString(viewsText, "")
			newRecVideo.ViewCount = -1
			cnt, err := strconv.ParseInt(viewsTextStripped, 10, 64)
			newRecVideo.ViewCount = cnt
			if err != nil {
				newRecVideo.ViewCount = -1
			}
			return false
		})

		if goodRec {
			video.InfoJSON.RecommendedVideos = append(video.InfoJSON.RecommendedVideos, newRecVideo)
		}
	})
	//fmt.Println(video.InfoJSON.RecommendedVideos)
}
func parseUploaderInfo(video *Video, document *goquery.Document) {
	uploaderID := document.Find("meta[itemprop='channelId']")
	video.InfoJSON.UploaderID = uploaderID.AttrOr("content", "")
	video.InfoJSON.UploaderURL = ""
	document.Find("div.yt-user-info").Each(func(i int, s *goquery.Selection) {
		uploader := s.Text()
		uploader = strings.Replace(uploader, "\u200b", "", -1)
		video.InfoJSON.Uploader = strings.TrimSpace(uploader)

	})
	/*
		document.Find("a").Each(func(i int, s *goquery.Selection) {
			if name, _ := s.Attr("class"); name == "yt-uix-sessionlink       spf-link " {
				uploader := s.Text()
				if strings.Contains(uploader, "https://www.youtube.com/watch?v=") == false && strings.Contains(uploader, "https://youtu.be") == false {
					video.InfoJSON.Uploader = uploader
				}
				uploaderID, _ := s.Attr("href")
				if strings.Contains(uploaderID, "/channel/") == true {
					video.InfoJSON.UploaderID = uploaderID[9:len(uploaderID)]
					video.InfoJSON.UploaderURL = "https://www.youtube.com" + uploaderID
				}
			}
		})*/
}

func parseLikeDislike(video *Video, document *goquery.Document) {
	document.Find("button.like-button-renderer-like-button").EachWithBreak(func(i int, s *goquery.Selection) bool {
		likeCount := strings.TrimSpace(s.Text())
		video.InfoJSON.LikeCount = cast.ToInt64(strings.Replace(likeCount, ",", "", -1))
		return false // break on first button (sometimes there are two)
	})
	document.Find("button.like-button-renderer-dislike-button").EachWithBreak(func(i int, s *goquery.Selection) bool {
		dislikeCount := strings.TrimSpace(s.Text())
		video.InfoJSON.DislikeCount = cast.ToInt64(strings.Replace(dislikeCount, ",", "", -1))
		return false // break on first button (sometimes there are two)
	})
}

func parseDatePublished(video *Video, document *goquery.Document) {
	document.Find("meta[itemprop='datePublished']").Each(func(i int, s *goquery.Selection) {
		date, _ := s.Attr("content")
		date = strings.Replace(date, "-", "", -1)
		video.InfoJSON.UploadDate = date
	})
}

func addFormats(video *Video) {
	for _, rawFormat := range video.RawFormats {
		tmpFormat := Format{}
		//fmt.Println(rawFormat)
		for k, v := range rawFormat {
			switch k {
			case "bitrate":
				tmpFormat.Bitrate, _ = strconv.ParseFloat(v[0], 64)
			case "clen":
				tmpFormat.Clen, _ = strconv.ParseFloat(v[0], 64)
			case "eotf":
				tmpFormat.EOTF = v[0]
			case "fps":
				tmpFormat.Fps, _ = strconv.ParseFloat(v[0], 64)
			case "index":
				tmpFormat.Index = v[0]
			case "init":
				tmpFormat.Init = v[0]
			case "itag":
				tmpFormat.FormatID = v[0]
				if v[0] == "82" || v[0] == "83" || v[0] == "84" ||
					v[0] == "85" || v[0] == "100" || v[0] == "101" ||
					v[0] == "102" {
					tmpFormat.FormatNote = "3D"
					tmpFormat.Format = tmpFormat.FormatID + " - " + tmpFormat.FormatNote
				} else if v[0] == "91" || v[0] == "92" ||
					v[0] == "93" || v[0] == "94" || v[0] == "95" ||
					v[0] == "96" || v[0] == "132" || v[0] == "151" {
					tmpFormat.FormatNote = "HLS"
					tmpFormat.Format = tmpFormat.FormatID + " - " + tmpFormat.FormatNote
				} else if v[0] == "139" || v[0] == "140" ||
					v[0] == "141" || v[0] == "256" || v[0] == "258" ||
					v[0] == "325" || v[0] == "328" || v[0] == "249" ||
					v[0] == "250" || v[0] == "251" {
					tmpFormat.FormatNote = "DASH audio"
					tmpFormat.Format = tmpFormat.FormatID + " - " + tmpFormat.FormatNote
				} else if v[0] == "133" || v[0] == "134" ||
					v[0] == "135" || v[0] == "136" || v[0] == "137" ||
					v[0] == "138" || v[0] == "160" || v[0] == "212" ||
					v[0] == "264" || v[0] == "298" || v[0] == "299" ||
					v[0] == "266" || v[0] == "167" || v[0] == "168" ||
					v[0] == "169" || v[0] == "170" || v[0] == "218" ||
					v[0] == "219" || v[0] == "278" || v[0] == "242" ||
					v[0] == "245" || v[0] == "244" || v[0] == "243" ||
					v[0] == "246" || v[0] == "247" || v[0] == "248" ||
					v[0] == "271" || v[0] == "272" || v[0] == "302" ||
					v[0] == "303" || v[0] == "308" || v[0] == "313" ||
					v[0] == "315" {
					tmpFormat.FormatNote = "DASH video"
					tmpFormat.Format = tmpFormat.FormatID + " - " + tmpFormat.FormatNote
				} else {
					tmpFormat.Format = tmpFormat.FormatID + " - " + tmpFormat.Type
				}
			case "lmt":
				tmpFormat.Lmt, _ = strconv.ParseFloat(v[0], 64)
			case "primaries":
				tmpFormat.Primaries = v[0]
			case "quality":
				fallthrough
			case "quality_label":
				tmpFormat.QualityLabel = v[0]
			case "size":
				tmpFormat.Size = v[0]
				sizes := strings.Split(v[0], "x")
				tmpFormat.Width, _ = strconv.ParseFloat(sizes[0], 64)
				tmpFormat.Height, _ = strconv.ParseFloat(sizes[1], 64)
			case "type":
				tmpFormat.Type = v[0]
				s := strings.Index(v[0], "/")
				e := strings.Index(v[0], ";")
				tmpFormat.Ext = v[0][s+1 : e]
			case "url":
				tmpFormat.URL = v[0]
			}
		}
		if cmdFlags.RemoveRedundantData {
			tmpFormat.URL = ""
			tmpFormat.Clen = 0
			tmpFormat.Index = ""
			tmpFormat.Init = ""
			tmpFormat.Lmt = 0
			tmpFormat.Type = ""
			tmpFormat.Format = ""
			tmpFormat.EOTF = ""
			tmpFormat.Primaries = ""
			tmpFormat.Size = ""
		}
		video.InfoJSON.Formats = append(video.InfoJSON.Formats, tmpFormat)
	}
}

func parseFormats(video *Video) {
	codecCount := 0
	if l, ok := video.playerArgs["adaptive_fmts"]; ok {
		formats := strings.Split(l.(string), ",")
		for _, format := range formats {
			if len(format) > 3 {
				args, _ := url.ParseQuery(format)
				video.RawFormats = append(video.RawFormats, args)
				codecCount++
			}
		}
	} else {
		if l, ok := video.playerArgs["url_encoded_fmt_stream_map"]; ok {
			formats := strings.Split(l.(string), ",")
			for _, format := range formats {
				if len(format) > 3 {
					args, _ := url.ParseQuery(format)
					video.RawFormats = append(video.RawFormats, args)
					codecCount++
				}
			}
		}
	}
	//fmt.Println("parseFormats")
	//fmt.Println(video.RawFormats)
	addFormats(video)
}

func parseTags(video *Video, document *goquery.Document) {
	document.Find("meta[property='og:video:tag']").Each(func(i int, s *goquery.Selection) {
		tag, _ := s.Attr("content")
		video.InfoJSON.Tags = append(video.InfoJSON.Tags, tag)
	})
}

func parseIsAdsEnabled(video *Video, document *goquery.Document) {
	video.InfoJSON.IsAdsEnabled = false
	adPlacements, ok1 := video.playerResponse["adPlacements"].([]interface{})
	if ok1 {
		if len(adPlacements) > 0 {
			video.InfoJSON.IsAdsEnabled = true
		}
	}
}

func parseRegionsAllowed(video *Video, document *goquery.Document) {
	/*  <div id="watch7-headline" class="clearfix">
	    <span class="standalone-collection-badge-renderer-text"><a href="/results?sp=EiG4AQHCARtDaElKWDVWUkliTTVIUlVSYWpmNlZQUEZVSTQ%253D&amp;search_query=Kfar+Saba" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=vcm0W8eEL4WT1wKh6ZGoAg" >Kfar Saba</a></span>
	    <span class="standalone-collection-badge-renderer-text"><b><a href="/playlist?list=PLhyKYa0YJ_5ABU4r0U2Mcj_Gj32UN80zX" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=SMm0W7rCBMrJgQfNo6zQBw" >Extra History</a></b> S22 • E1</span>
	*/
	watch7Headline := document.Find("meta[itemprop='regionsAllowed']")
	list, ok := watch7Headline.Attr("content")
	if ok {
		if len(list) > 1 {
			video.InfoJSON.RegionsAllowed = strings.Split(list, ",")
		}
	}
	//fmt.Println(video.InfoJSON.HeadlineBadges)
}

func parseInteractionCount(video *Video, document *goquery.Document) {
	/*  <div id="watch7-headline" class="clearfix">
	    <span class="standalone-collection-badge-renderer-text"><a href="/results?sp=EiG4AQHCARtDaElKWDVWUkliTTVIUlVSYWpmNlZQUEZVSTQ%253D&amp;search_query=Kfar+Saba" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=vcm0W8eEL4WT1wKh6ZGoAg" >Kfar Saba</a></span>
	    <span class="standalone-collection-badge-renderer-text"><b><a href="/playlist?list=PLhyKYa0YJ_5ABU4r0U2Mcj_Gj32UN80zX" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=SMm0W7rCBMrJgQfNo6zQBw" >Extra History</a></b> S22 • E1</span>
	*/
	watch7Headline := document.Find("meta[itemprop='interactionCount']")
	value, ok := watch7Headline.Attr("content")
	video.InteractionCount = 0
	if ok {
		if len(value) >= 1 {
			video.InteractionCount, _ = strconv.ParseInt(value, 10, 64)
		}
	}
	//fmt.Println(video.InfoJSON.HeadlineBadges)
}

func parseHeadlineBadge(video *Video, document *goquery.Document) {
	/*  <div id="watch7-headline" class="clearfix">
	    <span class="standalone-collection-badge-renderer-text"><a href="/results?sp=EiG4AQHCARtDaElKWDVWUkliTTVIUlVSYWpmNlZQUEZVSTQ%253D&amp;search_query=Kfar+Saba" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=vcm0W8eEL4WT1wKh6ZGoAg" >Kfar Saba</a></span>
	    <span class="standalone-collection-badge-renderer-text"><b><a href="/playlist?list=PLhyKYa0YJ_5ABU4r0U2Mcj_Gj32UN80zX" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=SMm0W7rCBMrJgQfNo6zQBw" >Extra History</a></b> S22 • E1</span>
	*/
	watch7Headline := document.Find("#watch7-headline")
	badges := watch7Headline.Find("span.standalone-collection-badge-renderer-text")
	badges.Each(func(i int, s *goquery.Selection) {
		s.Find("a").Each(func(j int, r *goquery.Selection) {
			newBadge := HeadlineBadge{}
			newBadge.Text = r.Text()
			newBadge.URL, _ = r.Attr("href")
			video.InfoJSON.HeadlineBadges = append(video.InfoJSON.HeadlineBadges, newBadge)
		})
	})

	//fmt.Println(video.InfoJSON.HeadlineBadges)
}

func parseCaptionAuthors(video *Video, document *goquery.Document) {

	/*<h4 class="title">
	    Caption authors (Italian)
	  </h4>
	  <ul class="content watch-info-tag-list">
	      <li><a href="/channel/UCioW4kXoGhMJHB3XUmS4pJA" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=yau0W-XNFJSo1wKAmpvYCQ&amp;feature=video-credit" >Ambra Figini</a></li>
	      <li><a href="/channel/UCg_ETpernP8ZXX9yKxvei_w" class=" yt-uix-sessionlink      spf-link " data-sessionlink="ei=yau0W-XNFJSo1wKAmpvYCQ&amp;feature=video-credit" >thomas rushton</a></li>
	  </ul>
	*/

	document.Find("li.watch-meta-item").EachWithBreak(func(j int, s *goquery.Selection) bool {
		title := s.Find("h4").Text()
		title = strings.TrimSpace(title)

		s.Find("ul.watch-info-tag-list").EachWithBreak(func(j int, t *goquery.Selection) bool {
			newCredit := Credit{}
			newCredit.Title = title
			newCredit.Author = t.Text()
			newCredit.Author = strings.TrimSpace(newCredit.Author)
			hadLinks := false
			t.Find("a.yt-uix-sessionlink").EachWithBreak(func(j int, l *goquery.Selection) bool {
				newCredit.URL = l.AttrOr("href", "")
				newCredit.Author = l.Text()
				newCredit.Author = strings.TrimSpace(newCredit.Author)
				video.InfoJSON.Credits = append(video.InfoJSON.Credits, newCredit)
				hadLinks = true
				return true
			})
			if !hadLinks {
				video.InfoJSON.Credits = append(video.InfoJSON.Credits, newCredit)
			}
			return true
		})
		return true
	})

}

func parseViewCount(video *Video, document *goquery.Document) {
	document.Find("div").Each(func(i int, s *goquery.Selection) {
		if name, _ := s.Attr("class"); name == "watch-view-count" {
			viewCount := s.Text()
			video.InfoJSON.ViewCount = cast.ToInt64(regLeaveOnlyDigits.ReplaceAllString(viewCount, ""))
			if video.InfoJSON.ViewCount == 0 && video.InteractionCount > 0 {
				video.InfoJSON.ViewCount = video.InteractionCount
			}
		}
	})
}

func parseCategory(video *Video, document *goquery.Document) {
	m := regexpGetCategory.FindAllStringSubmatch(video.RawHTML, -1)
	if len(m) == 1 && len(m[0]) == 2 {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(m[0][1]))
		if err != nil {
			panic(err)
		}
		video.InfoJSON.Category = doc.Find("a").Text()
	}
}

func parseAgeLimit(video *Video, document *goquery.Document) {
	watch7Headline := document.Find("meta[property='og:restrictions:age']")
	age, ok := watch7Headline.Attr("content")
	video.InfoJSON.AgeLimit = 0
	if ok {
		if len(age) > 1 {
			video.InfoJSON.AgeLimit = cast.ToFloat64(regLeaveOnlyDigits.ReplaceAllString(age, ""))
			if video.InfoJSON.AgeLimit == 0 {
				video.InfoJSON.AgeLimit = 18
			}
		}
	}
	/*m := regexpGetAgeLimit.FindAllStringSubmatch(video.RawHTML, -1)
	if len(m) == 1 && len(m[0]) == 2 {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(m[0][1]))
		if err != nil {
			panic(err)
		}
		isLicense := doc.Find("a").Text()
		if strings.Contains(isLicense, "Age-restricted video (based on Community Guidelines)") == true {
			video.InfoJSON.AgeLimit = 18
		}
	}*/
}

func parseDuration(video *Video) {
	if l, ok := video.playerArgs["length_seconds"]; ok {
		dur, _ := strconv.ParseInt(l.(string), 10, 64)
		video.InfoJSON.Duration = dur
	}
}

func parseUnavailable(video *Video, document *goquery.Document) {
	/* <h1 id="unavailable-message" class="message">
	          &quot;www.MMO-CheatShop.com ...&quot; is no longer available due to a copyright claim by Jagex Ltd..

	</h1>*/
	if !video.GotPlayerResponse {
		unavailableMessage := document.Find("#unavailable-message")

		if unavailableMessage.Length() > 0 {
			video.InfoJSON.UnavailableMessage = strings.TrimSpace(unavailableMessage.Text())
		}
	}
}

func parseLicense(video *Video) {
	m := regexpGetLicense.FindAllStringSubmatch(video.RawHTML, -1)
	if len(m) == 1 && len(m[0]) == 2 {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(m[0][1]))
		if err != nil {
			panic(err)
		}
		video.InfoJSON.License = doc.Find("a").Text()
	}
}

func parseVariousInfo(video *Video, document *goquery.Document) {
	t := time.Now()
	video.InfoJSON.FetchedDate = fmt.Sprintf("%d%02d%02d%02d%02d%02d",
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second())

	video.InfoJSON.ID = video.ID

	video.InfoJSON.Description = video.Description
	video.InfoJSON.Annotations = ""
	video.InfoJSON.Thumbnail = ""
	video.InfoJSON.WebpageURL = ""

	if !cmdFlags.RemoveRedundantData {
		video.InfoJSON.Thumbnail = video.Thumbnail
		video.InfoJSON.WebpageURL = "https://www.youtube.com/watch?v=" + video.ID
	}

	video.InfoJSON.AllowEmbed = false

	if playabilityStatus, ok := video.playerResponse["playabilityStatus"].(map[string]interface{}); ok {
		if val, ok2 := playabilityStatus["playableInEmbed"]; ok2 {
			video.InfoJSON.AllowEmbed, _ = val.(bool)
		}
	}
	video.InfoJSON.IsCrawlable = true
	if vidDetails, ok1 := video.playerResponse["videoDetails"].(map[string]interface{}); ok1 {
		/*"averageRating": 4.7090907,
		"allowRatings": true,
		"viewCount": "2243702",
		"author": "The Key of Awesome",
		"isPrivate": false,
		"isUnpluggedCorpus": false,
		"isLiveContent": false
		*/
		if val, ok2 := vidDetails["isCrawlable"]; ok2 {
			video.InfoJSON.IsCrawlable, _ = val.(bool)
		}
		if val, ok2 := vidDetails["isLiveContent"]; ok2 {
			video.InfoJSON.IsLiveContent, _ = val.(bool)
		}
		if val, ok2 := vidDetails["averageRating"]; ok2 {
			video.InfoJSON.AverageRating = val.(float64)
		}

	}
	parseRecommendedVideos(video, document)
	parseCaptionAuthors(video, document)
	parseHeadlineBadge(video, document)
	parseRegionsAllowed(video, document)
	parseIsAdsEnabled(video, document)
	parseIsCommentsEnabled(video, document)
	parseUploaderInfo(video, document)
	parseLikeDislike(video, document)
	parseDatePublished(video, document)
	parseLicense(video)
	parseInteractionCount(video, document)
	parseFormats(video)
	parseTags(video, document)
	parseCategory(video, document)
	parseViewCount(video, document)
	parseAgeLimit(video, document)
	parseDuration(video)
	parseUnavailable(video, document)
}

func parseThumbnailURL(video *Video, document *goquery.Document) {
	// extract thumbnail url
	document.Find("meta[property='og:image']").Each(func(i int, s *goquery.Selection) {
		thumbnailURL, _ := s.Attr("content")
		video.Thumbnail = strings.Replace(thumbnailURL, "https", "http", -1)
	})
}

func parseTitle(video *Video, document *goquery.Document) {
	// extract title
	title := strings.TrimSpace(document.Find("#eow-title").Text())
	video.InfoJSON.Title = title
	video.Title = strings.Replace(title, " ", "_", -1)
	video.Title = strings.Replace(video.Title, "/", "_", -1)
}

// HTTPRequestCustomUserAgent get with custom user agent
func HTTPRequestCustomUserAgent(url, userAgent string, transport *http.Transport) (resp *http.Response, err error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}

	if len(userAgent) > 1 {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Del("Accept-Encoding")

	client := &http.Client{Transport: transport}
	resp, error := client.Do(req)
	return resp, error
}

func parseHTML(video *Video) {
	//var workers sync.WaitGroup
	//workers.Add(5)
	// request video html page
	// use desktop user agent
	//userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.100 Safari/537.36"
	userAgent := "" //"Go-http-client/1.1"
	html, err := HTTPRequestCustomUserAgent("https://www.youtube.com/watch?v="+video.ID+"&gl=US&hl=en&disable_polymer=1&has_verified=1&bpctr=9999999999", userAgent, video.HTTPTransport)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		removeVideoFiles(video)
		runtime.Goexit()
	}
	// check status, exit if != 200
	if html.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Status code error for %s: %d %s", video.ID, html.StatusCode, html.Status)
		removeVideoFiles(video)
		runtime.Goexit()
	}
	body, err := ioutil.ReadAll(html.Body)
	// store raw html in video struct

	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	if isDebug {
		ioutil.WriteFile(baseFileName+".rawHTML.html", body, 0644)
	}

	//ioutil.WriteFile(baseFileName+".rawHTML.html", body, 0644)
	video.RawHTML = string(body)
	// start goquery in the page
	document, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		removeVideoFiles(video)
		runtime.Goexit()
	}
	parseTitle(video, document)
	parseDescription(video, document)
	parseThumbnailURL(video, document)
	//parseYTInitialData(video, document)
	parsePlayerArgs(video, document)
	//workers.Wait()
	parseVariousInfo(video, document)
	defer html.Body.Close()
}

func genPath(video *Video) {
	// create directory if it doesnt exist
	//fmt.Println(video.Path)
	if _, err := os.Stat(video.Path); err != nil { // os.IsNotExist(err)
		err = os.MkdirAll(video.Path, 0755)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			runtime.Goexit()
		}
	}

}

func addSubToJSON(video *Video, langCode string, directURL string, isDefault bool) {
	pIsDefault := &isDefault
	if !isDefault {
		pIsDefault = nil
	}
	if len(directURL) > 0 || cmdFlags.RemoveRedundantData {
		sub := Subtitle{"", "xml", pIsDefault}
		video.InfoJSON.Subtitles[langCode] = append(video.InfoJSON.Subtitles[langCode], sub)
	} else {
		urlXML := "http://www.youtube.com/api/timedtext?lang=" + langCode + "&v=" + video.ID
		urlTTML := "http://www.youtube.com/api/timedtext?lang=" + langCode + "&v=" + video.ID + "&fmt=ttml&name="
		urlVTT := "http://www.youtube.com/api/timedtext?lang=" + langCode + "&v=" + video.ID + "&fmt=vtt&name="
		video.InfoJSON.Subtitles[langCode] = append(video.InfoJSON.Subtitles[langCode], Subtitle{urlXML, "xml", pIsDefault}, Subtitle{urlTTML, "ttml", pIsDefault}, Subtitle{urlVTT, "vtt", pIsDefault})
	}
}

func downloadSub(video *Video, langCode string, lang string, directURL string, isDefault bool) {
	addSubToJSON(video, langCode, directURL, isDefault)
	//logger.Println("Downloading " + lang + " subtitle.." + "[" + langCode + "]")
	// generate subtitle URL
	url := "https://www.youtube.com/api/timedtext?lang=" + langCode + "&v=" + video.ID
	if len(directURL) > 0 {
		url = directURL
	}

	//color.Println(color.Yellow("[") + color.Magenta("~") + color.Yellow("]") + color.Yellow("[") + color.Cyan(video.ID) + color.Yellow("]") + color.Green(" Downloading ") + color.Yellow(lang) + color.Green(" subtitle.."))
	logger.Println("[~][" + video.ID + "] GOT SUBTITLES |" + langCode + "|" + lang)
	// get the data
	userAgent := ""
	resp, err := HTTPRequestCustomUserAgent(url, userAgent, video.HTTPTransport)

	//resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
	defer resp.Body.Close()
	// create the file
	baseFileName := video.Path + video.ID
	if !cmdFlags.DeepDirFormat {
		baseFileName += "_" + video.Title
	}

	out, err := os.Create(baseFileName + "." + langCode + ".xml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
	defer out.Close()
	// write the body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		runtime.Goexit()
	}
}

func fetchSubsList(video *Video) {
	// request subtitles list
	var tracks Tracklist

	json := video.playerResponse

	//fmt.Println("text =", json["captions"])

	captionsMap, ok := json["captions"].(map[string]interface{})
	if ok {
		playerCaptionsTracklistRendererMap, ok2 := captionsMap["playerCaptionsTracklistRenderer"].(map[string]interface{})
		captionTracksMap, ok3 := playerCaptionsTracklistRendererMap["captionTracks"].([]interface{})

		defaultSubIndex := -1

		if ok2 {
			video.InfoJSON.AllowSubContrib = false
			if _, ok := playerCaptionsTracklistRendererMap["contribute"]; ok {
				video.InfoJSON.AllowSubContrib = true
			}
			/* "audioTracks": [
			   {
			     "captionTrackIndices": [
			       0,
			       1,
			       2,
			       3,
			       4,
			       5,
			       6,
			       7,
			       8
			     ],
			     "defaultCaptionTrackIndex": 1,
			     "visibility": "UNKNOWN",
			     "hasDefaultTrack": true
			   }*/
			if audioTracks, ok4 := playerCaptionsTracklistRendererMap["audioTracks"].([]interface{}); ok4 {
				for _, element := range audioTracks {

					e := element.(map[string]interface{})
					iHasDefaultTrack, ok4 := e["hasDefaultTrack"]
					iDefaultSubIndex, ok5 := e["defaultCaptionTrackIndex"]
					if ok4 && ok5 && iHasDefaultTrack.(bool) {
						defaultSubIndex = (int)(iDefaultSubIndex.(float64))
					}
				}
			}
		}
		if ok2 && ok3 {

			for i, element := range captionTracksMap {
				e := element.(map[string]interface{})
				//fmt.Println("Element=", element)
				//fmt.Println(e["languageCode"])
				//fmt.Println(e["kind"])
				//fmt.Println(e["isTranslatable"])
				//fmt.Println(e["baseUrl"])
				//fmt.Println(e["vssId"])
				//fmt.Println((e["name"].(map[string]interface{}))["simpleText"])
				done := false
				isDefault := ""

				if i == defaultSubIndex {
					isDefault = "true"
				}
				if val, ok := e["kind"]; ok {
					if val.(string) == "asr" {
						asrTrack := Track{"a." + e["languageCode"].(string), (e["name"].(map[string]interface{}))["simpleText"].(string), isDefault, "asr", e["baseUrl"].(string)}
						tracks.Tracks = append(tracks.Tracks, asrTrack)
						done = true
					}
				}
				if !done {
					asrTrack := Track{e["languageCode"].(string), (e["name"].(map[string]interface{}))["simpleText"].(string), isDefault, "", e["baseUrl"].(string)}
					tracks.Tracks = append(tracks.Tracks, asrTrack)
				}
			}
		}
	}

	for _, track := range tracks.Tracks {
		isDefault := false

		if track.LangDefault == "true" {
			isDefault = true
		}

		downloadSub(video, track.LangCode, track.Lang, track.DirectURL, isDefault)
	}
}

func checkFiles(video *Video) bool {
	var oldPath string
	var newPath string

	//if isDebug {
	//	video.Path = "data/"
	//	return
	//}
	//generate both paths, new format and old format for checking later
	firstPart := video.ID[:cmdFlags.SubPathLen]
	secondPart := video.ID[cmdFlags.SubPathLen : 2*cmdFlags.SubPathLen]
	newPath = firstPart + "/" + secondPart + "/"

	firstChar := video.ID[:1]
	oldPath = firstChar + "/" + video.ID + "/"

	checkOldPathFormat := false
	checkNewPathFormat := false

	if cmdFlags.DeepDirFormat {
		video.Path = newPath
		checkNewPathFormat = true
	} else {
		video.Path = oldPath
		checkOldPathFormat = true
	}

	// if flag DualFormatExistCheck is set check both paths
	if cmdFlags.DualFormatExistCheck {
		checkNewPathFormat = true
		checkOldPathFormat = true
	}

	//calculate the minimal number of needed files to check if file was already downloaded
	//this should probably be change to verify existance of files via extentions based on flags (jpg,xml)
	needFileCount := 1
	if cmdFlags.GetAnnotation {
		needFileCount++
	}
	if cmdFlags.GetDescription {
		needFileCount++
	}
	if cmdFlags.GetThumbnail {
		needFileCount++
	}

	videoNotNeeded := false
	if checkOldPathFormat {
		files, err := ioutil.ReadDir(oldPath)
		if err == nil && len(files) > 0 {
			for _, file := range files {
				if strings.HasSuffix(file.Name(), ".info.json") && file.Size() > 0 {
					videoNotNeeded = true
				}
			}
		}
	}

	if checkNewPathFormat && !videoNotNeeded {
		fileInfo, err := os.Stat(newPath + video.ID + ".info.json")
		if err == nil && fileInfo.Size() > 0 {
			videoNotNeeded = true
		}
	}

	if videoNotNeeded {
		//color.Println(color.Yellow("[") + color.Red("!") + color.Yellow("]") + color.Yellow("[") + color.Cyan(video.ID) + color.Yellow("]") + color.Red(" This video has already been archived!"))
		logger.Println("[!][" + video.ID + "] ALR.HAD")
		return true
	}
	return false
}

func processSingleID(ID string, count *int64) {

	decrement := func(count *int64) { *count-- }

	defer decrement(count)

	video := new(Video)
	video.HTTPTransport = getRandTransport()
	video.ID = ID
	video.InfoJSON.Init()
	video.InfoJSON.Subtitles = make(map[string][]Subtitle)
	video.InfoJSON.Annotations = ""
	video.playerArgs = make(map[string]interface{})
	if len(ID) < 6 || len(ID) > 15 {
		logInfo("-", video, "Error Video ID incorrect lenght.")
		return
	}
	isRemoteRun := len(cmdFlags.MasterServer) > 0

	if isRemoteRun || !checkFiles(video) {
		if !isRemoteRun {
			genPath(video)
			logInfo("-", video, "START")
		}

		//logInfo("~", video, "Parsing infos, description, title and thumbnail..")
		parseHTML(video)
		//logInfo("~", video, "Fetching subtitles..")
		if len(video.InfoJSON.UnavailableMessage) == 0 { // video is available
			if !isRemoteRun {
				fetchSubsList(video)
			}
		} else {
			logInfo("~", video, "Video UnavailableMessage  "+strings.Replace(video.InfoJSON.UnavailableMessage, "\n", " ", -1))
		}
		//logInfo("~", video, "Fetching annotations..")
		if !isRemoteRun && cmdFlags.GetAnnotation {
			fetchAnnotations(video)
		}
		//logInfo("~", video, "Writing informations locally..")
		if !isRemoteRun && cmdFlags.GetThumbnail {
			logInfo("~", video, "Downloading thumbnail..")
			downloadThumbnail(video)
		}

		if isRemoteRun {
			writeFilesForRemoteUpload(video)
		} else {
			writeFiles(video)
		}
		if !isRemoteRun {
			logInfo("✓", video, "DONE")
		} else {
			if combinedOutFile.recID%50 == 0 {
				logInfo("✓", video, "DONE"+fmt.Sprintf(" %d videos processed", combinedOutFile.recID))
			}
		}
	}
}

var tildePre = "[~]["
var checkPre = "[✓]["
var dashPre = "[-]["

func logInfo(info string, video *Video, log string) {
	if info == "-" {
		logger.Println(dashPre + video.ID + "] " + log)
	} else if info == "✓" {
		logger.Println(checkPre + video.ID + "] " + log)
	} else {
		logger.Println(tildePre + video.ID + "] " + log)
	}
}

func processList(maxConc int64, path string, listOfIDsBuffer *string, gzipOutput *bytes.Buffer) {
	var count int64
	count = 0

	isRemoteRun := len(cmdFlags.MasterServer) > 0
	var file *os.File
	var err error
	var scanner *bufio.Scanner
	if !isRemoteRun {
		file, err = os.Open(path)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()
		scanner = bufio.NewScanner(file)
	} else {
		scanner = bufio.NewScanner(strings.NewReader(*listOfIDsBuffer))
	}

	// scan the list line by line
	//var videosFile *os.File
	var videosFileGz *gzip.Writer
	var fileCreateError error

	if isRemoteRun {
		/*videosFile, fileCreateError = os.Create(tempFile)
		if fileCreateError != nil {
			fmt.Fprintf(os.Stderr, "Error creating videos.json.gz: %v\n", fileCreateError)
			runtime.Goexit()
		}
		defer videosFile.Close()*/
		videosFileGz, fileCreateError = gzip.NewWriterLevel(gzipOutput, gzip.BestCompression)
		if fileCreateError != nil {
			fmt.Fprintf(os.Stderr, "Error creating gzip writer videos.json.gz: %v\n", fileCreateError)
			runtime.Goexit()
		}
		defer videosFileGz.Close()
		combinedOutFile.recID = 0
		combinedOutFile.outFile = videosFileGz
		combinedOutFile.outFile.Write([]byte("["))
	}

	for scanner.Scan() {
		for true {
			if count < maxConc {
				break
			} else {
				time.Sleep(20 * time.Millisecond)
			}
		}
		count++
		go processSingleID(scanner.Text(), &count)

	}
	// log if error
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}
	i := 0
	fmt.Printf("Waiting for threads to complete\n")
	for i < 5000 {
		i++
		if count <= 0 {
			break
		} else {
			//fmt.Fprintf(os.Stderr, "count: %d\n", count)
			time.Sleep(20 * time.Millisecond)
		}
	}

	if isRemoteRun {
		combinedOutFile.mutex.Lock()
		defer combinedOutFile.mutex.Unlock()
		combinedOutFile.outFile.Write([]byte("]"))
		videosFileGz.Close()
		fmt.Println(fmt.Sprintf("Batch downloaded %d videos processed", combinedOutFile.recID))
	}

}

func populateFlags(args []string) {
	/*
		DeepDirFormat        bool
		GetAutoSub           bool
		GetDescription       bool
		GetAnnotation        bool
		GetThumbnail         bool
		ShortFileName        bool
		DualFormatExistCheck bool
	*/

	flag.BoolVar(&cmdFlags.DeepDirFormat, "DeepDirFormat", cmdFlags.DeepDirFormat, "a bool")
	flag.BoolVar(&cmdFlags.GetAutoSub, "GetAutoSub", cmdFlags.GetAutoSub, "a bool")
	flag.BoolVar(&cmdFlags.GetDescription, "GetDescription", cmdFlags.GetDescription, "a bool")
	flag.BoolVar(&cmdFlags.GetAnnotation, "GetAnnotation", cmdFlags.GetAnnotation, "a bool")
	flag.BoolVar(&cmdFlags.GetThumbnail, "GetThumbnail", cmdFlags.GetThumbnail, "a bool")
	flag.BoolVar(&cmdFlags.DualFormatExistCheck, "DualFormatExistCheck", cmdFlags.DualFormatExistCheck, "a bool")
	flag.BoolVar(&cmdFlags.RemoveRedundantData, "RemoveRedundantData", cmdFlags.RemoveRedundantData, "a bool")
	flag.BoolVar(&cmdFlags.UseSockProxy, "UseSockProxy", cmdFlags.UseSockProxy, "a bool")
	flag.StringVar(&cmdFlags.MasterServer, "MasterServer", cmdFlags.MasterServer, "a string")
	flag.IntVar(&cmdFlags.SubPathLen, "SubPathLen", cmdFlags.SubPathLen, "an int")
	flag.Int64Var(&cmdFlags.Concurrency, "Concurrency", cmdFlags.Concurrency, "an int")

	cmdFlags.WorkerUUID, _ = newUUID()
	flag.Parse()
}

var controlClient = &http.Client{Timeout: 300 * time.Second}

func getJSON(url string, target interface{}) (int, error) {
	r, err := controlClient.Get(url)

	if err != nil {
		return 500, err
	}
	defer r.Body.Close()

	return r.StatusCode, json.NewDecoder(r.Body).Decode(target)
}

func argumentParsing(args []string) {
	var maxConc int64

	populateFlags(args)
	tailArgs := flag.Args()

	//fmt.Println(cmdFlags)
	//fmt.Println(tailArgs)
	//log.Fatal("exit")

	maxConc = cmdFlags.Concurrency

	if cmdFlags.UseSockProxy {
		initProxyTransports((int)(maxConc))
	} else {
		initTransports((int)(maxConc))
	}
	var count int64
	count = 1

	fileWithIds := ""
	isRemoteRun := len(cmdFlags.MasterServer) > 0

	if len(tailArgs) > 0 {
		fileWithIds = tailArgs[0]
	}
	batchID := ""
	batchUUID := ""

	continueRunning := true
	var gzipBuffer bytes.Buffer
	for continueRunning {
		start := time.Now()
		if isRemoteRun {
			//fetch block from server
			newWorkBatch := new(workBatch) // or &Foo{}
			batchUnitURL := cmdFlags.MasterServer + "/getBatchWorkUnit?version=" + strconv.FormatInt(int64(formatVersion), 10) + "&workerUUID=" + url.QueryEscape(cmdFlags.WorkerUUID)
			//fmt.Println(batchUnitURL)
			statusCode, err := getJSON(batchUnitURL, newWorkBatch)
			if err != nil || (len(newWorkBatch.VideoIDS) == 0) {
				if err != nil {
					fmt.Fprintf(os.Stderr, "Unable to fetch getBatchWorkUnit: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "Recieved empty work unit, status %d message %s \n", statusCode, newWorkBatch.Message)
				}
				sleepTime := 20
				fmt.Printf("Sleeping %d seconds and then will retry\n", sleepTime)
				time.Sleep(time.Duration(sleepTime) * time.Second)
				continue
			}
			batchID = newWorkBatch.BatchID
			batchUUID = newWorkBatch.BatchUUID

			fmt.Println(fmt.Sprintf("Recieved batch unit for download BatchID:%s (%s) - %d videos", batchID, batchUUID, len(newWorkBatch.VideoIDS)))

			var temp bytes.Buffer

			for idx, element := range newWorkBatch.VideoIDS {
				if idx >= 1 {
					temp.WriteString("\n")
				}
				temp.WriteString(element)
			}
			downloadIDs := temp.String()
			gzipBuffer.Reset()
			processList(maxConc, "", &downloadIDs, &gzipBuffer)
		} else {
			if _, err := os.Stat(fileWithIds); err == nil {
				processList(maxConc, fileWithIds, nil, nil)
			} else {
				processSingleID(fileWithIds, &count)
			}
		}

		if isRemoteRun {

			// get the size
			size := gzipBuffer.Len

			extraParams := map[string]string{
				"batchID":    batchID,
				"batchUUID":  batchUUID,
				"videoCount": strconv.FormatInt(combinedOutFile.recID, 10),
				"workerUUID": cmdFlags.WorkerUUID,
				"version":    strconv.FormatInt(int64(formatVersion), 10),
			}
			try := 0
			maxTries := 250
			allDone := false
			//NewPostFile("http://localhost:8080/upload", extraParams, "file", "D:\\new_tv\\Outlander.S04E13.720p.WEB.H264-METCON[eztv].mkv")
			for allDone == false && try < maxTries {
				try++
				fmt.Printf("Uploading block %s (%s) with %d records, %d bytes, attempt %d\n", batchID, batchUUID, combinedOutFile.recID, size, try)

				response, err := sendMultipart(cmdFlags.MasterServer+"/submitBatchWorkUnit", extraParams, "data", &gzipBuffer)
				if err == nil && response.StatusCode == 200 {
					fmt.Printf("Block %s uploaded\n", batchID)
					allDone = true
				} else {
					if err != nil {
						fmt.Printf("error uploading %v\n", err)
					} else {
						body, _ := ioutil.ReadAll(response.Body)
						fmt.Printf("error uploading response code %d %s\n", response.StatusCode, body)
					}
					fmt.Printf("Block %s upload failed, try %d, will retry after sleep\n", batchID, try)
					sleepTime := 5 * try
					if sleepTime > 300 {
						sleepTime = 300
					}
					fmt.Printf("Sleeping %d seconds\n", sleepTime)
					time.Sleep(time.Duration(sleepTime) * time.Second)
				}
			}
			if !allDone {
				fmt.Fprintf(os.Stderr, "Upload failed after retries, stopping program\n")
				os.Exit(1)
			}
		}
		color.Println(color.Cyan("Cycle done in ") + color.Yellow(time.Since(start)))

		if !isRemoteRun {
			continueRunning = false
		}
	}
}

// JSONMarshalIndentNoEscapeHTML allow proper json formatting
func JSONMarshalIndentNoEscapeHTML(i interface{}, prefix string, indent string) ([]byte, error) {
	buf := &bytes.Buffer{}
	encoder := json.NewEncoder(buf)
	encoder.SetEscapeHTML(false)
	if isDebug {
		encoder.SetIndent(prefix, indent)
	} else {
		encoder.SetIndent("", " ")
	}
	err := encoder.Encode(i)
	return buf.Bytes(), err
}

func main() {

	runtime.GOMAXPROCS(164)
	rand.Seed(time.Now().Unix())

	argumentParsing(os.Args[1:])

}
