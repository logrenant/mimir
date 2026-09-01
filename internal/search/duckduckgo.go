package search

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// browserUserAgent is what every outbound DuckDuckGo request sends. The
// endpoints answer differently — up to and including refusing — for a
// non-browser agent, so the health probe must use this one too.
const browserUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func (c *Client) searchHTML(ctx context.Context, query string) ([]Result, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.SearchTimeout)
	defer cancel()

	form := url.Values{}
	form.Add("q", query)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.DuckDuckGoHTMLURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		// Treat non-200 as failure so fallback kicks in
		return nil, errors.New("searchHTML: non-200 status")
	}

	return parseHTML(resp.Body)
}

func parseHTML(body io.Reader) ([]Result, error) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, err
	}

	var results []Result
	doc.Find(".web-result").Each(func(i int, s *goquery.Selection) {
		a := s.Find(".result__title a")
		if a.Length() == 0 {
			return
		}
		title := strings.TrimSpace(a.Text())
		href, _ := a.Attr("href")

		// Extract uddg parameter
		u, err := url.Parse(href)
		if err != nil {
			return
		}

		uddg := u.Query().Get("uddg")
		if uddg == "" {
			return
		}

		targetURL, err := url.QueryUnescape(uddg)
		if err != nil {
			return
		}
		if !strings.HasPrefix(targetURL, "http://") && !strings.HasPrefix(targetURL, "https://") {
			return
		}

		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())
		if title != "" && targetURL != "" {
			results = append(results, Result{
				Title:   title,
				URL:     targetURL,
				Snippet: snippet,
			})
		}
	})

	return results, nil
}

func (c *Client) searchLite(ctx context.Context, query string) ([]Result, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.cfg.SearchTimeout)
	defer cancel()

	form := url.Values{}
	form.Add("q", query)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.cfg.DuckDuckGoLiteURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("searchLite: non-200 status")
	}

	return parseLite(resp.Body)
}

func parseLite(body io.Reader) ([]Result, error) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, err
	}

	var results []Result
	doc.Find("a.result-link").Each(func(i int, s *goquery.Selection) {
		title := strings.TrimSpace(s.Text())
		href, _ := s.Attr("href")

		if !strings.HasPrefix(href, "http://") && !strings.HasPrefix(href, "https://") {
			return
		}

		tr := s.Closest("tr")
		nextTr := tr.Next()
		snippet := strings.TrimSpace(nextTr.Find(".result-snippet").Text())

		if title != "" && href != "" {
			results = append(results, Result{
				Title:   title,
				URL:     href,
				Snippet: snippet,
			})
		}
	})

	return results, nil
}
