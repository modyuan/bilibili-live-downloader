package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/bitly/go-simplejson"
)

const API_LIVE = "https://api.live.bilibili.com"
const API_ROOM_ID = API_LIVE + "/room/v1/Room/room_init?id=%v"
const API_PLAY_URL = API_LIVE + "/xlive/web-room/v1/playUrl/playUrl?cid=%v&qn=%v&platform=%v&ptype=%v"

func readableSize(fileSize int64) string {
	if fileSize < 1024 {
		return fmt.Sprintf("%v B", fileSize)
	} else if fileSize < 1024*1024 {
		return fmt.Sprintf("%.2f KiB", float32(fileSize)/1024.0)
	} else if fileSize < 1024*1024*1024 {
		return fmt.Sprintf("%.2f MiB", float32(fileSize)/1024.0/1024.0)
	} else {
		return fmt.Sprintf("%.2f GiB", float32(fileSize)/1024.0/1024.0/1024.0)
	}
}

func getRoomId(urlId int) (roomId int) {
	var resp *http.Response
	url := fmt.Sprintf(API_ROOM_ID, urlId)
	// resp, err := Request(url, "GET", nil)
	resp, err := http.Get(url)
	if err != nil {
		log.Fatalf("fail to get url %v, reason: %v", API_ROOM_ID, err)
		return 0
	} else {
		defer resp.Body.Close()
		var info, err = ioutil.ReadAll(resp.Body)
		if err != nil {
			log.Fatal(err)
			return 0
		}
		j, err := simplejson.NewJson(info)
		if err != nil {
			log.Fatalf("fail to parse json, content: %v", string(info))
			return 0
		}
		if j.Get("code").MustInt() != 0 {
			log.Fatalf("fail to get roomId, reason: %v", j.Get("message").MustString())
		}
		return j.Get("data").Get("room_id").MustInt()
	}
}

func getPlayUrl(roomId int) string {
	url := fmt.Sprintf(API_PLAY_URL, roomId, 10000, "h5", 16)
	r, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
		return ""
	}
	defer r.Body.Close()
	b, _ := ioutil.ReadAll(r.Body)
	j, _ := simplejson.NewJson(b)
	if j.Get("code").MustInt() != 0 {
		log.Fatalf("fail to get %v, msg: %v", url, j.Get("reason").MustString())
	}
	arr := j.Get("data").Get("durl")
	return arr.GetIndex(0).Get("url").MustString()
}

func getIndexByUrl(url string) string {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		i = -1
	}
	i2 := strings.LastIndex(url, "?")
	filename := url
	if i2 > 0 {
		filename = url[i+1 : i2]
	}
	i3 := strings.LastIndex(filename, ".")
	if i3 >= 0 {
		filename = filename[0:i3]
	}
	i4 := strings.LastIndex(filename, "-")
	if i4 > 0 {
		filename = filename[i4+1:]
	}
	return filename
}

func getExtByUrl(url string) string {
	i := strings.LastIndex(url, "/")
	if i < 0 {
		return ""
	}
	url = url[i:]
	i2 := strings.Index(url, ".")
	if i2 < 0 {
		return ""
	}
	url = url[i2+1:]
	i3 := strings.Index(url, "?")
	if i3 < 0 {
		return ""
	}
	return url[0:i3]
}

func getM3u8(url string) (videoUrl []string, index []string) {
	if url == "" {
		return nil, nil
	}

	i := strings.LastIndex(url, "/")
	if i < 0 {
		log.Fatalf("fail to find path part in url: %v", url)
	}
	baseurl := url[0 : i+1]

	r, err := http.Get(url)
	if err != nil {
		log.Printf("fail to get url: %v, error: %v", url, err)
		return nil, nil
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		return nil, nil
	}
	b, err2 := ioutil.ReadAll(r.Body)
	if err2 != nil {
		log.Printf("fail to read m3u8: %v, reason: %v", url, err2)
	}
	if strings.Index(string(b), "#EXTM3U") < 0 {
		log.Fatalf("fail to parse m3u8, content: %v", string(b))
	}
	s := strings.Split(string(b), "\n")
	videoUrl = make([]string, 0, 5)
	index = make([]string, 0, 5)
	for i := 0; i < len(s); i++ {
		s[i] = strings.Trim(s[i], "\n")
		s[i] = strings.Trim(s[i], "\r")
		if len(s[i]) == 0 || s[i][0] == '#' {
			continue
		}
		if strings.Index(s[i], "http") == 0 {
			videoUrl = append(videoUrl, s[i])
		} else {
			videoUrl = append(videoUrl, baseurl+s[i])
		}
		index = append(index, getIndexByUrl(s[i]))

	}
	if len(videoUrl) == 1 && getExtByUrl(videoUrl[0]) == "m3u8" {
		return getM3u8(videoUrl[0])
	} else {
		return videoUrl, index
	}

}

func download(url string) (ts []byte, err error) {
	r, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return b, nil

}

func downloadToFile(urlId int, filename string) {

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)

	fmt.Printf("开始下载直播视频, 直播间号[%v], 按Ctrl+C中止录制\n", urlId)
	roomId := getRoomId(urlId)
	// fmt.Printf("roomId: %v\n", roomId)
	s := getPlayUrl(roomId)

	f, err := os.Create(filename)
	if err != nil {
		log.Fatalf("fail to create file, %v", err)
	}
	defer f.Close()

	var dataCount int64 = 0
	var lastTs string = ""
	for {
		videoUrl, index := getM3u8(s)
		if videoUrl != nil {
			for i := 0; i < len(videoUrl); i++ {
				if index[i] <= lastTs {
					continue
				}
				lastTs = index[i]
				ts, err := download(videoUrl[i])
				if err != nil {
					log.Printf("fail to read from ts: %v", videoUrl[i])
					return
				}
				dataCount += int64(len(ts))
				f.Write(ts)
				log.Printf("write %v", index[i])
			}
		} else {
			fmt.Printf("直播间已关闭, 程序自动退出, 文件名: %v, 大小: %v\n", filename, readableSize(dataCount))
			break
		}

		select {
		case <-c:
			{
				f.Close()
				fmt.Printf("\n程序主动退出, 文件名: %v，大小: %v\n", filename, readableSize(dataCount))
				os.Exit(0)
			}
		default:
			//fmt.Println("nothing")
		}
	}
}

func main() {
	usage := "用法: " + os.Args[0] + " [-o filename] 直播间号"
	filename := flag.String("o", "out.mp4", "输出文件名称")

	d, _ := time.ParseDuration("1s")
	http.DefaultClient.Timeout = d

	flag.Parse()
	s := flag.Args()
	if s == nil || len(s) == 0 {
		fmt.Println("需要指定直播间号码")
		fmt.Println(usage)
		flag.PrintDefaults()
		os.Exit(1)
	}
	num, err := strconv.ParseInt(s[0], 10, 32)
	if err != nil {
		fmt.Println("直播间号码格式不正确")
		fmt.Println(usage)
		flag.PrintDefaults()
	}

	_, err = os.Stat(*filename)
	if err == nil {
		fmt.Printf("文件[%v]已存在\n", *filename)
		os.Exit(1)
	} else {

		downloadToFile(int(num), *filename)
	}
}
