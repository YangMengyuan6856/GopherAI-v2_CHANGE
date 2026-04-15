package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// SearchResult 单条搜索结果
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// DuckDuckGoSearch 通过 DuckDuckGo HTML 版本执行搜索，无需 API Key
func DuckDuckGoSearch(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}

	form := url.Values{}
	form.Set("q", query)
	form.Set("kl", "cn-zh") // 中文区域

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://html.duckduckgo.com/html/",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, fmt.Errorf("create search request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search returned status %d", resp.StatusCode)
	}

	return parseDuckDuckGoHTML(resp.Body, maxResults)
}

// parseDuckDuckGoHTML 解析 DuckDuckGo HTML 搜索结果页
func parseDuckDuckGoHTML(r io.Reader, maxResults int) ([]SearchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse html failed: %w", err)
	}

	var results []SearchResult
	var crawler func(*html.Node)
	crawler = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}

		if n.Type == html.ElementNode && n.Data == "div" && hasClass(n, "result") {
			sr := extractSearchResult(n)
			if sr.Title != "" && sr.URL != "" {
				results = append(results, sr)
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			crawler(c)
		}
	}
	crawler(doc)

	return results, nil
}

// extractSearchResult 从单个搜索结果 div 中提取标题、URL、摘要
func extractSearchResult(n *html.Node) SearchResult {
	var sr SearchResult
	var extract func(*html.Node)
	extract = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			sr.Title = getTextContent(node)
			sr.URL = getAttr(node, "href")
			// DuckDuckGo 的链接可能是重定向格式，提取真实 URL
			if strings.Contains(sr.URL, "uddg=") {
				if parsed, err := url.Parse(sr.URL); err == nil {
					if realURL := parsed.Query().Get("uddg"); realURL != "" {
						sr.URL = realURL
					}
				}
			}
		}
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__snippet") {
			sr.Snippet = getTextContent(node)
		}

		for c := node.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(n)
	return sr
}

// FormatSearchResults 将搜索结果格式化为 LLM 可读的文本
func FormatSearchResults(results []SearchResult) string {
	if len(results) == 0 {
		return "未找到相关搜索结果"
	}
	var sb strings.Builder
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("   链接: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("   摘要: %s\n", r.Snippet))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// FetchURLContent 获取指定 URL 的网页内容，返回提取后的纯文本
func FetchURLContent(ctx context.Context, rawURL string, maxChars int) (string, error) {
	if maxChars <= 0 {
		maxChars = 4000
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch url failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch returned status %d", resp.StatusCode)
	}

	// 限制读取大小（最多 1MB）
	limitedReader := io.LimitReader(resp.Body, 1024*1024)
	doc, err := html.Parse(limitedReader)
	if err != nil {
		return "", fmt.Errorf("parse html failed: %w", err)
	}

	text := extractVisibleText(doc)
	text = cleanWhitespace(text)

	if utf8.RuneCountInString(text) > maxChars {
		runes := []rune(text)
		text = string(runes[:maxChars]) + "\n...[内容已截断]"
	}

	if text == "" {
		return "无法提取网页内容（页面可能使用了 JavaScript 动态加载）", nil
	}

	return text, nil
}

// 需要跳过的 HTML 标签（噪音内容）
var skipTags = map[string]bool{
	"script":   true,
	"style":    true,
	"noscript": true,
	"nav":      true,
	"footer":   true,
	"header":   true,
	"aside":    true,
	"svg":      true,
	"iframe":   true,
	"form":     true,
}

// extractVisibleText 从 HTML DOM 树中提取可见文本
func extractVisibleText(n *html.Node) string {
	var sb strings.Builder
	var extract func(*html.Node)
	extract = func(node *html.Node) {
		if node.Type == html.ElementNode && skipTags[node.Data] {
			return
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		// 块级元素前后加换行
		if node.Type == html.ElementNode {
			switch node.Data {
			case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "blockquote", "pre", "article", "section":
				sb.WriteString("\n")
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}

	// 优先提取 <body> 的内容
	body := findNode(n, "body")
	if body != nil {
		extract(body)
	} else {
		extract(n)
	}
	return sb.String()
}

// findNode 在 DOM 树中查找指定标签的第一个节点
func findNode(n *html.Node, tag string) *html.Node {
	if n.Type == html.ElementNode && n.Data == tag {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := findNode(c, tag); found != nil {
			return found
		}
	}
	return nil
}

// cleanWhitespace 清理多余空白
func cleanWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	var cleaned []string
	prevEmpty := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !prevEmpty {
				cleaned = append(cleaned, "")
				prevEmpty = true
			}
			continue
		}
		prevEmpty = false
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// hasClass 检查 HTML 节点是否包含指定 CSS class
func hasClass(n *html.Node, class string) bool {
	for _, attr := range n.Attr {
		if attr.Key == "class" {
			for _, c := range strings.Fields(attr.Val) {
				if c == class {
					return true
				}
			}
		}
	}
	return false
}

// getAttr 获取 HTML 节点的指定属性值
func getAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

// getTextContent 获取节点内的纯文本内容
func getTextContent(n *html.Node) string {
	var sb strings.Builder
	var extract func(*html.Node)
	extract = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(n)
	return strings.TrimSpace(sb.String())
}
