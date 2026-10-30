package content

import (
	"bytes"
	"html"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxBodyBytes  = 200 * 1024
	MaxTitleRunes = 200
)

var (
	commentRe     = regexp.MustCompile(`(?s)<!--.*?-->`)
	scriptStyleRe = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>|<script\b[^>]*>.*$|<style\b[^>]*>.*$`)
	tagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	titleRe       = regexp.MustCompile(`(?is)<title\b[^>]*>(.*?)</title\s*>`)
	metaRe        = regexp.MustCompile(`(?is)<meta\b[^>]*>`)
	attrRe        = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

// ValidHTML 只做文档要求的最低限度结构检查。
func ValidHTML(raw []byte) bool {
	lower := bytes.ToLower(raw)
	return bytes.Contains(lower, []byte("<html")) || bytes.Contains(lower, []byte("<!doctype"))
}

// IsHTMLFilename 判断扩展名，大小写不敏感。
func IsHTMLFilename(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm")
}

// Title 抽取 <title> 文本；缺失时退化为文件名词干。
func Title(raw []byte, filename string) string {
	if m := titleRe.FindSubmatch(raw); len(m) == 2 {
		t := collapse(stripTags(string(m[1])))
		if t != "" {
			return truncateRunes(t, MaxTitleRunes)
		}
	}
	return truncateRunes(TitleFromFilename(filename), MaxTitleRunes)
}

// TitleFromFilename 去掉扩展名，保留原始大小写。
func TitleFromFilename(filename string) string {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == "" {
		return filename
	}
	return base
}

// BodyText 去掉 script/style/注释/标签，折叠空白，截断到 200KB。
func BodyText(raw []byte) string {
	s := string(raw)
	s = commentRe.ReplaceAllString(s, " ")
	s = scriptStyleRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = collapse(s)
	return truncateBytes(s, MaxBodyBytes)
}

// MetaLesson 读取 <meta name="lesson" content="3">，返回 3。
func MetaLesson(raw []byte) (int, bool) {
	for _, tag := range metaRe.FindAllString(string(raw), -1) {
		attrs := parseAttrs(tag)
		if !strings.EqualFold(attrs["name"], "lesson") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(attrs["content"]))
		if err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

func parseAttrs(tag string) map[string]string {
	out := make(map[string]string)
	for _, m := range attrRe.FindAllStringSubmatch(tag, -1) {
		key := strings.ToLower(m[1])
		val := m[2]
		if val == "" {
			val = m[3]
		}
		if val == "" {
			val = m[4]
		}
		out[key] = val
	}
	return out
}

func stripTags(s string) string {
	s = commentRe.ReplaceAllString(s, " ")
	s = scriptStyleRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	return html.UnescapeString(s)
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	used := 0
	for i := range s {
		if i > max {
			return s[:used]
		}
		used = i
	}
	return s
}
