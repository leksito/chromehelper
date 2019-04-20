package main

import (
	"chromehelper/chromeclient"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strings"
)

var re = regexp.MustCompile(`(; isg|; l)=[A-Za-z0-9\-_\.]+`)

func prepareCookies(cookies string) string {
	return re.ReplaceAllString(cookies, "")
}

func doRequest(request *http.Request, client *http.Client) (int, []map[string]string, []byte, error) {

	_, ok := request.Header["Cookie"]
	if ok == true {
		request.Header["Cookie"][0] = prepareCookies(request.Header["Cookie"][0])
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, []byte{}, err
	}
	defer response.Body.Close()

	code := response.StatusCode

	var responseHeaders []map[string]string
	for name, values := range response.Header {
		for _, value := range values {
			responseHeaders = append(responseHeaders, map[string]string{
				"name":  name,
				"value": value,
			})
		}
	}

	var reader io.ReadCloser
	switch response.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(response.Body)
		defer reader.Close()
	default:
		reader = response.Body
	}

	body, err := ioutil.ReadAll(reader)

	dump, _ := httputil.DumpRequest(request, true)
	fmt.Println(string(dump))
	dump, _ = httputil.DumpResponse(response, false)
	fmt.Println(string(dump))

	return code, responseHeaders, body, nil
}

func handleRequest(id int, requestID string, request chromeclient.ChromeRequest, client *http.Client) (chromeclient.FetchFulfillRequestParams, error) {
	req, _ := request.ToHTTPRequest()
	code, headers, body, err := doRequest(req, client)
	if err != nil {
		return chromeclient.FetchFulfillRequestParams{}, err
	}
	params := chromeclient.FetchFulfillRequestParams{
		RequestID:       requestID,
		ResponseCode:    code,
		ResponseHeaders: headers,
		Body:            base64.StdEncoding.EncodeToString(body),
	}
	return params, err
}

func poller(in <-chan chromeclient.RequestPausedResponse, out chan<- interface{}, chromeClient chromeclient.ChromeClient) {
	clients := make(map[string]*http.Client)
	for response := range in {
		chromeClient.ID++
		request := response.Params.Request

		proxy, ok := request.Headers["__proxy__"]
		if ok == false {
			proxy = ""
		} else {
			delete(clients, "__proxy__")
		}

		client, ok := clients[proxy]
		if ok == false {
			client = createHTTPClient(proxy)
			clients[proxy] = client
		}
		go func(out chan<- interface{}, id int, requestId string, request chromeclient.ChromeRequest, client *http.Client) {
			params, err := handleRequest(id, requestId, request, client)
			if err != nil {
				log.Println(err)
			} else {
				out <- params
			}

		}(out, chromeClient.ID, response.Params.RequestID, request, client)
	}
}

func sender(out <-chan interface{}, chromeClient chromeclient.ChromeClient) {
	for params := range out {
		if err := chromeClient.Send(params); err != nil {
			log.Println(err)
		}
	}
}

func proxyFunc(request *http.Request) (*url.URL, error) {
	proxyStr, ok := request.Header["__proxy__"]
	if ok == false {
		return nil, nil
	}
	delete(request.Header, "__proxy__")
	proxyURL, err := url.Parse("//" + proxyStr[0])
	if err != nil {
		log.Println(err)
	}
	return proxyURL, err
}

func createHTTPClient(proxyStr string) *http.Client {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar: nil,
	}
	if proxyStr != "" {
		_, err := url.Parse(proxyStr)
		if err != nil {
			log.Println(err)
		}

		transport := &http.Transport{
			Proxy:               proxyFunc,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost: 100,
		}
		client.Transport = transport
	}
	return client
}

func main() {

	chromeClient, err := chromeclient.NewChromeClient("http://127.0.0.1:9222")
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer chromeClient.Ws.Close()

	if err = chromeClient.FetchEnable(); err != nil {
		log.Println(err)
		os.Exit(1)
	}

	pending := make(chan chromeclient.RequestPausedResponse)
	defer close(pending)

	complete := make(chan interface{})
	defer close(complete)

	go poller(pending, complete, chromeClient)
	go sender(complete, chromeClient)

	for {
		_, responseJSON, err := chromeClient.Ws.ReadMessage()
		if err != nil || !strings.Contains(string(responseJSON), "Fetch.requestPaused") {
			continue
		}

		response := chromeclient.RequestPausedResponse{}
		json.Unmarshal(responseJSON, &response)

		pending <- response
	}

}
