package common

import (
	"strings"
	"testing"
)

const sampleIndexPage = `<!doctype html>
<html lang="en">
  <head>
    <title>New API</title>
    <script>var injectedLater = "</head>";</script>
  </head>
  <body><div id="root"></div></body>
</html>`

func TestInjectBeforeHeadCloseInsertsSnippetBeforeLastHeadClose(t *testing.T) {
	snippet := `<meta name="verify" content="abc123" />`
	out := string(InjectBeforeHeadClose([]byte(sampleIndexPage), snippet))

	if !strings.Contains(out, snippet) {
		t.Fatalf("snippet missing from output:\n%s", out)
	}
	// 片段必须在 </head> 之前，且紧贴最后一个 </head>
	snippetPos := strings.Index(out, snippet)
	headClosePos := strings.LastIndex(out, "</head>")
	if snippetPos < 0 || headClosePos < 0 || snippetPos > headClosePos {
		t.Fatalf("snippet not placed before </head>: snippet=%d head=%d\n%s", snippetPos, headClosePos, out)
	}
	// 页面脚本字符串里的 </head> 不能被当成注入点
	if !strings.Contains(out, `var injectedLater = "</head>";`) {
		t.Fatalf("original script content was altered:\n%s", out)
	}
	if !strings.HasSuffix(out, "</html>") && !strings.HasSuffix(out, "</html>\n") {
		t.Fatalf("page tail was damaged:\n%s", out)
	}
}

func TestInjectBeforeHeadCloseNoOps(t *testing.T) {
	page := []byte(sampleIndexPage)
	if got := string(InjectBeforeHeadClose(page, "")); got != string(page) {
		t.Fatal("empty snippet must not change the page")
	}
	if got := string(InjectBeforeHeadClose(page, "   \n\t ")); got != string(page) {
		t.Fatal("blank snippet must not change the page")
	}
	noHead := []byte("<html><body>dashboard</body></html>")
	if got := string(InjectBeforeHeadClose(noHead, "<script>x</script>")); got != string(noHead) {
		t.Fatal("page without </head> must be returned unchanged")
	}
}

func TestIndexPageHTMLReflectsOptionChanges(t *testing.T) {
	OptionMapRWMutex.Lock()
	// 本包的单测不会跑 InitOptionMap，OptionMap 可能是 nil
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	previous, hadPrevious := OptionMap[CustomHeadHTMLOptionKey]
	OptionMap[CustomHeadHTMLOptionKey] = ""
	OptionMapRWMutex.Unlock()
	defer func() {
		OptionMapRWMutex.Lock()
		if hadPrevious {
			OptionMap[CustomHeadHTMLOptionKey] = previous
		} else {
			delete(OptionMap, CustomHeadHTMLOptionKey)
		}
		OptionMapRWMutex.Unlock()
	}()

	page := []byte(sampleIndexPage)
	if got := string(IndexPageHTML(page)); got != string(page) {
		t.Fatal("no snippet configured: page must be served as-is")
	}

	setSnippet := func(v string) {
		OptionMapRWMutex.Lock()
		OptionMap[CustomHeadHTMLOptionKey] = v
		OptionMapRWMutex.Unlock()
	}

	setSnippet(`<meta name="first" content="1" />`)
	first := string(IndexPageHTML(page))
	if !strings.Contains(first, `name="first"`) {
		t.Fatalf("first snippet missing:\n%s", first)
	}

	// 改了配置必须立刻生效（缓存要跟着失效），不能继续吐旧片段
	setSnippet(`<meta name="second" content="2" />`)
	second := string(IndexPageHTML(page))
	if strings.Contains(second, `name="first"`) {
		t.Fatalf("stale snippet still present after change:\n%s", second)
	}
	if !strings.Contains(second, `name="second"`) {
		t.Fatalf("new snippet missing:\n%s", second)
	}

	// 清空配置后回到原始页面
	setSnippet("")
	if got := string(IndexPageHTML(page)); got != string(page) {
		t.Fatal("cleared snippet: page must be served as-is again")
	}
}

func TestValidateCustomHeadHTML(t *testing.T) {
	valid := []string{
		"",
		`<meta name="google-site-verification" content="x" />`,
		`<script src="https://example.com/widget.js"></script>`,
		`<link rel="stylesheet" href="/custom.css">`,
	}
	for _, v := range valid {
		if err := ValidateCustomHeadHTML(v); err != nil {
			t.Fatalf("expected valid, got error for %q: %v", v, err)
		}
	}

	invalid := []string{
		"<html><head></head><body></body></html>",
		"<meta name=\"x\" />\n</head>",
		strings.Repeat("a", customHeadHTMLMaxBytes+1),
	}
	for _, v := range invalid {
		if err := ValidateCustomHeadHTML(v); err == nil {
			t.Fatalf("expected validation error for %q", v)
		}
	}
}
