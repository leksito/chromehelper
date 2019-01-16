package main

import "encoding/json"
import "fmt"
import "io"
import "io/ioutil"
import "net/http/httputil"
import "log"
import "net/http"
import "net/url"
import "strings"
import "bytes"
import "compress/gzip"
import "crypto/tls"
import "os"
import "chromehelper/chromeclient"

func doRequest(request chromeclient.ChromeRequest, client *http.Client) (int, []chromeclient.ResponseHeader, []byte, error) {
	url, err := url.Parse(request.URL)
	if err != nil {
		log.Println(err)
	}

	request.Headers["Accept-Encoding"] = "gzip"
	request.Headers["Accept"] = "*/*"

	req, err := http.NewRequest(request.Method, url.String(),
		bytes.NewBuffer([]byte(request.PostData)))
	if err != nil {
		log.Println(err)
	}

	for key, value := range request.Headers {
		req.Header.Add(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, []byte{}, err
	}
	defer resp.Body.Close()

	code := resp.StatusCode

	var responseHeaders []chromeclient.ResponseHeader
	for name, values := range resp.Header {
		for _, value := range values {
			responseHeaders = append(responseHeaders, chromeclient.ResponseHeader{
				Name:  name,
				Value: value,
			})
		}
	}

	var reader io.ReadCloser
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		defer reader.Close()
	default:
		reader = resp.Body
	}

	body, err := ioutil.ReadAll(reader)

	dump, _ := httputil.DumpRequest(req, true)
	fmt.Println(string(dump))
	dump, _ = httputil.DumpResponse(resp, false)
	fmt.Println(string(dump))

	return code, responseHeaders, body, nil
}

func handleRequest(c chromeclient.ChromeClient, id int, requestId string, request chromeclient.ChromeRequest, client *http.Client) {
	code, headers, body, err := doRequest(request, client)
	if err != nil {
		log.Println("request failed")
		log.Println(err)
		return
	}

	if err = c.FulfillRequest(requestId, code, headers, body); err != nil {
		log.Println(err)
	}

}

func Poller(in <-chan chromeclient.RequestPausedResponse, chromeClient chromeclient.ChromeClient) {
	clients := make(map[string]*http.Client)
	for response := range in {
		chromeClient.ID += 1
		request := response.Params.Request

		proxy, ok := request.Headers["__proxy__"]
		if ok == false {
			proxy = ""
		} else {
			delete(clients, "__proxy__")
		}

		for key, value := range clients {
			log.Printf("%s: %s\n", key, value)
		}

		client, ok := clients[proxy]
		if ok == false {
			client = createHttpClient(proxy)
			clients[proxy] = client
		}
		go handleRequest(chromeClient, chromeClient.ID, response.Params.RequestID, request, client)
	}
}

func createHttpClient(proxyStr string) *http.Client {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	if proxyStr != "" {
		proxyURL, err := url.Parse(proxyStr)
		if err != nil {
			log.Println(err)
		}

		transport := &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
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

	go Poller(pending, chromeClient)

	for {
		_, response_json, err := chromeClient.Ws.ReadMessage()
		if err != nil {
			continue
		}

		if !strings.Contains(string(response_json), "Fetch.requestPaused") {
			continue
		}

		response := chromeclient.RequestPausedResponse{}
		json.Unmarshal(response_json, &response)

		pending <- response
	}
}
