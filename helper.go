package main

import "encoding/json"
import "fmt"
import "io"
import "io/ioutil"
import "net/http/httputil"
import "net/http"
import "log"
import "net/url"
import "strings"
import "compress/gzip"
// import "crypto/tls"
import "os"
import "chromehelper/chromeclient"
// import "encoding/base65"

func doRequest(request *http.Request, client *http.Client) (int, []map[string]string, []byte, error) {

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

func Poller(in <-chan chromeclient.RequestPausedResponse, out chan<- interface{}, chromeClient chromeclient.ChromeClient) {
	client := newHttpClient()
	for response := range in {
		chromeClient.ID += 1
		request, _ := response.Params.Request.ToHTTPRequest()

		go func(out chan<- interface{}, id int, requestId string, request *http.Request, client *http.Client) {
			code, headers, body, err := doRequest(request, client)
			if err != nil {
				log.Println(err)
			}
			out <- chromeclient.NewFetchFulfillRequestParams(requestId, code, headers, body)
		}(out, chromeClient.ID, response.Params.RequestID, request, client)
	}
}

func Sender(out <-chan interface{}, chromeClient chromeclient.ChromeClient) {
	for params := range out {
		if err := chromeClient.Send(params); err != nil {
			log.Println(err)
		}
	}
}

func ProxyFunc(request *http.Request) (*url.URL, error) {
	proxyStr, ok := request.Header["__proxy__"]
	if ok == false {
		return nil, nil
	} else {
		delete(request.Header, "__proxy__")
	}
	proxyURL, err := url.Parse(proxyStr[0])
	if err != nil {
		log.Println(err)
	}
	return proxyURL, err
}

func newHttpClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// handle redirects, if there is it then do not do following location
			return http.ErrUseLastResponse
		},
        Transport: &http.Transport {
            Proxy: ProxyFunc,
        },
	}
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

	go Poller(pending, complete, chromeClient)
	go Sender(complete, chromeClient)

	for {
		_, response_json, err := chromeClient.Ws.ReadMessage()
		if err != nil || !strings.Contains(string(response_json), "Fetch.requestPaused") {
			continue
		}

		response := chromeclient.RequestPausedResponse{}
		json.Unmarshal(response_json, &response)

		pending <- response
	}

}
