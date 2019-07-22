package main

import (
	"chromehelper/chromeclient"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
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
	"time"
)

var re = regexp.MustCompile(`(; isg|; l)=[A-Za-z0-9\-_\.]+`)

func prepareCookies(cookies string) string {
	return re.ReplaceAllString(cookies, "")
}

func doRequest(request *http.Request, client *http.Client) (int, []map[string]string, []byte, error) {
	dump, _ := httputil.DumpRequest(request, true)
	fmt.Println(string(dump))

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
	// clients := make(map[string]*http.Client)
	client := createHTTPClient()

	for response := range in {
		chromeClient.ID++
		request := response.Params.Request

		// _, ok := request.Headers["__proxy__"]
		// if ok == false {
		// 	_ = ""
		// }

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

	fmt.Println(proxyStr)
	dump, _ := httputil.DumpRequest(request, true)
	fmt.Println(string(dump))

	proxyURL, err := url.Parse("//" + proxyStr[0])
	if err != nil {
		log.Println(err)
	}
	return proxyURL, err
}

func createHTTPClient() *http.Client {
	defaultRoundTripper := http.DefaultTransport
	defaultTransportPtr, ok := defaultRoundTripper.(*http.Transport)
	if !ok {
		panic(fmt.Sprintf("defaultRoundTripper not an *http.Transport"))
	}
	defaultTransport := *defaultTransportPtr
	defaultTransport.MaxIdleConns = 1000
	defaultTransport.MaxIdleConnsPerHost = 1000
	defaultTransport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true,
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout:   time.Second * 60,
		Jar:       nil,
		Transport: &defaultTransport,
	}
	return client
}

func main() {

	port := flag.String("port", "9222", "Chrome browser remote port. Default - `9222`")
	host := flag.String("host", "127.0.0.1", "Chrome browser remote host. Default - `http://127.0.0.1`")
	flag.Parse()

	patterns := flag.Args()

	chromeClient, err := chromeclient.NewChromeClient(host, port, patterns)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	defer chromeClient.Ws.Close()

	log.Println("Connected to " + chromeClient.Version["Browser"])

	if err = chromeClient.FetchEnable(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
	log.Println("Fetch enbled")

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
