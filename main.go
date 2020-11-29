/*
*    @author wangchunyan.wang
*    @date 11/28/20 10:08 PM
 */

// Package main 主要实现m3u8文件的下载和转码
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

var (
	m3u8Path   = flag.String("m3u8Path", "", "-m3u8Path http://xxx/xxx/index.m3u8")
	routineNum = flag.Int("routine_num", 20, "-routine_num 50")
	program    = flag.String("ffmpeg_path", "ffmpeg", "-ffmpeg_path /xxx/ffmpeg")
	host       string
	commonURL  string

	downloadFilesChan = make(chan []string, *routineNum)
)

const (
	m3u8File    = "index.m3u8"
	m3u8FileBak = m3u8File + ".bak"
	keyFile     = "key.key"
)

func getPosition() string {
	_, file, line, _ := runtime.Caller(1)
	return fmt.Sprintf("%s:%d", file, line)
}

func check() {
	if *m3u8Path == "" {
		fmt.Fprintf(os.Stderr, "must have m3u8 file path\n")
		os.Exit(1)
	}
	fields := strings.Split(*m3u8Path, "/")
	if len(fields) < 3 {
		fmt.Fprintf(os.Stderr, "invalid m3u8 file:%s\n", *m3u8Path)
		os.Exit(10)
	}
	host = strings.Join(fields[0:3], "/")
	commonURL = strings.Join(fields[3:len(fields)-1], "/")
	commonURL = "/" + commonURL + "/"
	fmt.Fprintf(os.Stdout, "host:%s,common url:%s\n", host, commonURL)
}

func download(url, file string) error {
	_, err := os.Stat(file)
	if err == nil {
		//fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
		fmt.Fprintf(os.Stdout, "file:%s exist\n", file)
		return nil
	}

	fmt.Fprintf(os.Stdout, "download:%s\n", url)
	fmt.Fprintf(os.Stdout, "cmd:wget -q %s -O %s\n", url, file)
	cmd := exec.Command("wget", "-q", url, "-O", file)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
		fmt.Fprintf(os.Stderr, "download:%s failed,err:%s\n", url, err.Error())
	}
	return err

	resp, err := http.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "http get file:%s failed,err:%s\n", *m3u8Path, err.Error())
		return err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read from http body failed,err:%s\n", err.Error())
		return err
	}
	err = ioutil.WriteFile(file, data, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "")
		return err
	}
	return nil
}

func downloadFromChan() {
	wg := &sync.WaitGroup{}
	mux := &sync.Mutex{}
	routines := 0
	for files := range downloadFilesChan {
		if files[0] == "" && files[1] == "" {
			break
		}
		fmt.Fprintf(os.Stdout, "begin dowload:%s\n", files[0])
		if len(files) == 4 {
			//fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
			fmt.Fprintf(os.Stdout, "all file:%s,now file id:%s\n", files[2], files[3])
		}
		wg.Add(1)

		for {
			mux.Lock()
			if routines >= *routineNum {
				mux.Unlock()
				time.Sleep(1 * time.Second)
				continue
			}
			routines++
			mux.Unlock()
			break
		}

		go func(files []string) {
			defer wg.Done()
			err := download(files[0], files[1])
			if err == nil {
				//fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
				fmt.Fprintf(os.Stdout, "download file:%s success\n", files[0])
				if len(files) == 4 {
					fmt.Fprintf(os.Stdout, "all file:%s,now file id:%s\n", files[2], files[3])
				}
			} else {
				fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
				fmt.Fprintf(os.Stdout, "download file:%s failed,err:%s\n", files[0], err.Error())
			}
			mux.Lock()
			routines--
			mux.Unlock()
		}(files)
	}
	wg.Wait()
}

func downloadM3u8Content() error {
	dstM3u8Content := ""
	data, err := ioutil.ReadFile(m3u8FileBak)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pos:%s\n", getPosition())
		fmt.Fprintf(os.Stderr, "read index.m3u8 failed,err:%s\n", err.Error())
		return err
	}
	lines := strings.Split(string(data), "\n")

	allFileNum := 0
	for _, line := range lines {
		if strings.Contains(line, ".ts") {
			allFileNum++
		}
	}
	fileIdx := 0
	for _, line := range lines {
		if strings.Contains(line, "URI=") {
			ks := strings.Split(line, "\"")
			if len(ks) == 3 {
				downloadFilesChan <- []string{host + ks[1], keyFile}
			}
			sps := strings.Split(line, "URI=")
			if len(sps) == 2 {
				dstM3u8Content += sps[0] + "URI=\"" + keyFile + "\""
			}
		} else if strings.Contains(line, ".ts") {
			sps := strings.Split(line, "/")
			fileIdx++
			downloadFilesChan <- []string{host + line, sps[len(sps)-1],
				fmt.Sprintf("%d", allFileNum), fmt.Sprintf("%d", fileIdx)}
			dstM3u8Content += sps[len(sps)-1] + "\n"
		} else {
			dstM3u8Content += line + "\n"
		}
	}
	ioutil.WriteFile(m3u8File, []byte(dstM3u8Content), 0666)
	downloadFilesChan <- []string{"", ""}
	return nil
}

func convertM3u8ToMP4() {
	dstFile := fmt.Sprintf("%d.mp4", time.Now().Unix())
	// ffmpeg -allowed_extensions ALL -protocol_whitelist "file,http,crypto,tcp" -i index.m3u8 -c copy 1.mp4
	cmd := exec.Command(*program, "-allowed_extensions", "ALL", "-protocol_whitelist", "file,http,crypto,tcp",
		"-i", m3u8File, "-c", "copy", dstFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()

	_, err := os.Stat(dstFile)
	if err == nil {
		fmt.Fprintf(os.Stdout, "file:%s exist,will remove ts file\n", dstFile)
		filepath.Walk("./", func(path string, info os.FileInfo, err error) error {
			if strings.HasSuffix(path, ".ts") {
				os.Remove(path)
			}
			return nil
		})
	}
	os.Remove(m3u8File)
	os.Remove(m3u8FileBak)
	os.Remove(keyFile)
}

func main() {
	flag.Parse()
	check()

	err := download(*m3u8Path, m3u8FileBak)
	if err != nil {
		fmt.Fprintf(os.Stderr, "download file:%s failed,err:%s\n", *m3u8Path, err.Error())
		os.Exit(1)
	} else {
		fmt.Fprintf(os.Stdout, "download file:%s success\n", *m3u8Path)
	}

	go downloadFromChan()

	err = downloadM3u8Content()
	if err != nil {
		fmt.Fprintf(os.Stderr, "downloadM3u8Content failedm,err:%s\n", err.Error())
		os.Exit(2)
	}
	convertM3u8ToMP4()
	fmt.Fprintf(os.Stdout, "download success.\n")
}
