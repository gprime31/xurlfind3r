package wayback

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/hueristiq/hqgolimit"
	"github.com/hueristiq/xurlfind3r/pkg/xurlfind3r/httpclient"
	"github.com/hueristiq/xurlfind3r/pkg/xurlfind3r/sources"
	"github.com/valyala/fasthttp"
)

type Source struct{}

var (
	limiter = hqgolimit.New(&hqgolimit.Options{
		RequestsPerMinute: 40,
	})
)

func (source *Source) Run(config *sources.Configuration, domain string) (URLsChannel chan sources.URL) {
	URLsChannel = make(chan sources.URL)

	go func() {
		defer close(URLsChannel)

		var err error

		getPagesReqURL := formatURL(domain, config.IncludeSubdomains) + "&showNumPages=true"

		limiter.Wait()

		var getPagesRes *fasthttp.Response

		getPagesRes, err = httpclient.SimpleGet(getPagesReqURL)
		if err != nil {
			return
		}

		var pages uint

		if err = json.Unmarshal(getPagesRes.Body(), &pages); err != nil {
			return
		}

		waybackURLs := [][]string{}

		for page := uint(0); page < pages; page++ {
			getURLsReqURL := fmt.Sprintf("%s&page=%d", formatURL(domain, config.IncludeSubdomains), page)

			limiter.Wait()

			var getURLsRes *fasthttp.Response

			getURLsRes, err = httpclient.SimpleGet(getURLsReqURL)
			if err != nil {
				return
			}

			var getURLsResData [][]string

			if err = json.Unmarshal(getURLsRes.Body(), &getURLsResData); err != nil {
				return
			}

			// check if there's results, wayback's pagination response
			// is not always correct when using a filter
			if len(getURLsResData) == 0 {
				break
			}

			// output results
			// Slicing as [1:] to skip first result by default
			waybackURLs = append(waybackURLs, getURLsResData[1:]...)
		}

		mediaURLRegex := regexp.MustCompile(`(?i)\.(apng|bpm|png|bmp|gif|heif|ico|cur|jpg|jpeg|jfif|pjp|pjpeg|psd|raw|svg|tif|tiff|webp|xbm|3gp|aac|flac|mpg|mpeg|mp3|mp4|m4a|m4v|m4p|oga|ogg|ogv|mov|wav|webm|eot|woff|woff2|ttf|otf|pdf)(?:\?|#|$)`)
		robotsURLsRegex := regexp.MustCompile(`^(https?)://[^ "]+/robots.txt$`)

		wg := &sync.WaitGroup{}

		for _, waybackURL := range waybackURLs {
			wg.Add(1)

			go func(waybackURL []string) {
				defer wg.Done()

				URL := waybackURL[1]

				if !sources.IsInScope(URL, domain, config.IncludeSubdomains) {
					return
				}

				URLsChannel <- sources.URL{Source: source.Name(), Value: URL}

				if mediaURLRegex.MatchString(URL) {
					return
				}

				if config.ParseWaybackRobots && robotsURLsRegex.MatchString(URL) {
					for robotsURL := range parseWaybackRobots(config, URL) {
						URLsChannel <- sources.URL{Source: source.Name() + ":robots", Value: robotsURL}
					}

					return
				}

				if config.ParseWaybackSource {
					for sourceURL := range parseWaybackSource(domain, URL) {
						if !sources.IsInScope(sourceURL, domain, config.IncludeSubdomains) {
							continue
						}

						URLsChannel <- sources.URL{Source: source.Name() + ":source", Value: sourceURL}
					}
				}
			}(waybackURL)
		}

		wg.Wait()
	}()

	return
}

func formatURL(domain string, includeSubdomains bool) (URL string) {
	if includeSubdomains {
		domain = "*." + domain
	}

	URL = fmt.Sprintf("http://web.archive.org/cdx/search/cdx?url=%s/*&output=json&collapse=urlkey&fl=timestamp,original,mimetype,statuscode,digest", domain)

	return
}

func getSnapshots(URL string) (snapshots [][2]string, err error) {
	getSnapshotsReqURL := fmt.Sprintf("https://web.archive.org/cdx/search/cdx?url=%s&output=json&fl=timestamp,original&collapse=digest", URL)

	var getSnapshotsRes *fasthttp.Response

	limiter.Wait()

	getSnapshotsRes, err = httpclient.SimpleGet(getSnapshotsReqURL)
	if err != nil {
		return
	}

	if getSnapshotsRes.Header.ContentLength() == 0 {
		return
	}

	if err = json.Unmarshal(getSnapshotsRes.Body(), &snapshots); err != nil {
		return
	}

	if len(snapshots) < 2 {
		return
	}

	snapshots = snapshots[1:]

	return
}

func getSnapshotContent(snapshot [2]string) (content string, err error) {
	var (
		timestamp = snapshot[0]
		URL       = snapshot[1]
	)

	getSnapshotContentReqURL := fmt.Sprintf("https://web.archive.org/web/%sif_/%s", timestamp, URL)

	limiter.Wait()

	var getSnapshotContentRes *fasthttp.Response

	getSnapshotContentRes, err = httpclient.SimpleGet(getSnapshotContentReqURL)
	if err != nil {
		return
	}

	content = string(getSnapshotContentRes.Body())

	if content == "" {
		return
	}

	snapshotNotFoundFingerprint := "This page can't be displayed. Please use the correct URL address to access"

	if strings.Contains(content, snapshotNotFoundFingerprint) {
		err = fmt.Errorf("%s", snapshotNotFoundFingerprint)

		return
	}

	return
}

func (source *Source) Name() string {
	return "wayback"
}
