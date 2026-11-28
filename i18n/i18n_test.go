package i18n

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The web console saves its own language codes (zhCN, zhTW, fr, ...) in the
// user setting; backend messages must follow them, and languages without a
// backend locale fall back to English.
func TestLanguageFromUserSetting(t *testing.T) {
	require.NoError(t, Init())
	for _, tc := range []struct {
		saved string
		want  string
	}{
		{"zhCN", LangZhCN},
		{"zhTW", LangZhTW},
		{"zh-HK", LangZhTW},
		{"zh_TW", LangZhTW},
		{"zh_HK", LangZhTW},
		{"zh-Hant-TW", LangZhTW},
		{"zh", LangZhCN},
		{"en", LangEn},
		{"fr", LangEn},
		{"ja", LangEn},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/chat/completions", nil)
		c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: tc.saved})
		assert.Equal(t, tc.want, StatedLang(c), tc.saved)
	}
}

// TokenAuth puts the user's settings into the request. A user who saved no
// language must not be loaded again, which would cost a cache or database read
// on every relay request; a request without loaded settings still loads them.
func TestStatedLangLoadsTheUserOnlyWithoutLoadedSettings(t *testing.T) {
	previous := userLangLoaderFunc
	t.Cleanup(func() { userLangLoaderFunc = previous })
	loads := 0
	SetUserLangLoader(func(int) string {
		loads++
		return "zhTW"
	})

	relay, _ := gin.CreateTestContext(httptest.NewRecorder())
	relay.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	relay.Set("id", 7)
	relay.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{})
	assert.Equal(t, "", StatedLang(relay))
	assert.Zero(t, loads)

	dashboard, _ := gin.CreateTestContext(httptest.NewRecorder())
	dashboard.Request = httptest.NewRequest("GET", "/api/user/self", nil)
	dashboard.Set("id", 7)
	assert.Equal(t, LangZhTW, StatedLang(dashboard))
	assert.Equal(t, 1, loads)
}

// A saved language or an Accept-Language header selects the language of a
// backend message. A reader who states neither gets DEFAULT_LANGUAGE, which is
// English when unset — protocol errors stay in English for API clients.
func TestBackendMessageLanguage(t *testing.T) {
	require.NoError(t, Init())
	t.Cleanup(func() { common.DefaultLanguage = "" })
	quota := map[string]any{"Remaining": "$0.10"}
	const english = "Insufficient user quota, remaining quota: $0.10"
	const chinese = "用户额度不足, 剩余额度: $0.10"
	for _, tc := range []struct {
		name            string
		defaultLanguage string
		saved           string
		acceptLanguage  string
		want            string
	}{
		{"nothing stated", "", "", "", english},
		{"no preference header", "", "", "*", english},
		{"nothing stated with a Chinese default", "zh-CN", "", "", chinese},
		{"nothing stated with an English default", "en", "", "", english},
		{"header wins over the default", "zh-CN", "", "en-US,en;q=0.9", english},
		{"header in a language without a locale", "zh-CN", "", "fr-FR", english},
		{"saved language wins over the header", "", "zhTW", "en-US", "使用者額度不足，剩餘額度：$0.10"},
		{"saved language", "zh-CN", "en", "", english},
	} {
		t.Run(tc.name, func(t *testing.T) {
			common.DefaultLanguage = tc.defaultLanguage
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.acceptLanguage != "" {
				c.Request.Header.Set("Accept-Language", tc.acceptLanguage)
			}
			if tc.saved != "" {
				c.Set(string(constant.ContextKeyUserSetting), dto.UserSetting{Language: tc.saved})
			}
			assert.Equal(t, tc.want, T(c, MsgQuotaUserInsufficient, quota))
		})
	}
}

// A key missing from a locale, or a template that does not parse, reaches
// clients as the raw key.
//
// Locale files may hold keys that keys.go does not declare: the web console and
// a large part of the API pass English source text as the key, and those keys
// are translated in place, so the check is "every declared key exists in every
// locale" rather than "the key sets are identical".
func TestLocalesRenderEveryKey(t *testing.T) {
	require.NoError(t, Init())
	keysFile, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	var declared []string
	ast.Inspect(keysFile, func(node ast.Node) bool {
		if spec, ok := node.(*ast.ValueSpec); ok {
			for _, value := range spec.Values {
				if literal, ok := value.(*ast.BasicLit); ok {
					key, err := strconv.Unquote(literal.Value)
					require.NoError(t, err)
					declared = append(declared, key)
				}
			}
		}
		return true
	})
	slices.Sort(declared)

	keysByLang := make(map[string][]string)
	for _, lang := range SupportedLanguages() {
		data, err := localeFS.ReadFile("locales/" + lang + ".yaml")
		require.NoError(t, err)
		var messages map[string]string
		require.NoError(t, yaml.Unmarshal(data, &messages))
		for key := range messages {
			keysByLang[lang] = append(keysByLang[lang], key)
		}
		slices.Sort(keysByLang[lang])
	}
	for _, lang := range SupportedLanguages() {
		for _, key := range declared {
			assert.Contains(t, keysByLang[lang], key, "%s is missing the declared key %s", lang, key)
		}
		for _, key := range keysByLang[lang] {
			text := Translate(lang, key, map[string]any{})
			// English locales store the English source text as the key, so the
			// rendered text legitimately equals the key there; Chinese locales
			// must actually translate it.
			if lang != LangEn {
				assert.NotEqual(t, key, text, "%s %s", lang, key)
			}
			assert.False(t, strings.Contains(text, "{{"), "%s %s: %s", lang, key, text)
		}
	}
}

// Web console responses carry the English source text as message_key plus its
// params, and the web console can translate them on its own. The message field
// is already rendered in the language the request stated, so plain API callers
// (scripts, chat bots) read the same wording the console shows; keys without a
// translation keep their English source text.
func TestWebConsoleMessageResponse(t *testing.T) {
	require.NoError(t, Init())
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "params",
			err:  common.NewMessage("Channel {{name}} (#{{id}}) does not exist", map[string]any{"name": "{{id}}", "id": 7}),
			want: `{"success":false,"message":"Channel {{id}} (#7) does not exist","message_key":"Channel {{name}} (#{{id}}) does not exist","message_params":{"id":7,"name":"{{id}}"}}`,
		},
		{
			name: "no params",
			err:  common.NewMessage("Invalid parameters"),
			want: `{"success":false,"message":"无效的参数","message_key":"Invalid parameters"}`,
		},
		{
			name: "plain error",
			err:  errors.New("record not found"),
			want: `{"success":false,"message":"record not found"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/channel/7", nil)
			c.Request.Header.Set("Accept-Language", "zh-CN")
			common.ApiError(c, tc.err)
			assert.JSONEq(t, tc.want, recorder.Body.String())
		})
	}
}

// Server log lines are English. DEFAULT_LANGUAGE=zh-CN prints the Chinese text of
// the lines that have one in common/log_text.zh-CN.json.
func TestServerLogLanguage(t *testing.T) {
	t.Cleanup(func() { common.SetLogLanguage("") })
	const line = "channel #%d has %d unfinished tasks"
	assert.Equal(t, "channel #7 has 2 unfinished tasks", common.LogText(line, 7, 2))
	common.SetLogLanguage("zh-CN")
	assert.Equal(t, "渠道 #7 未完成的任务有: 2", common.LogText(line, 7, 2))
	assert.Equal(t, "a line without a Chinese text: x", common.LogText("a line without a Chinese text: %s", "x"))
	assert.Equal(t, "任务进度轮询开始", common.LogText("task progress polling started"))
	common.SetLogLanguage("en")
	assert.Equal(t, "channel #7 has 2 unfinished tasks", common.LogText(line, 7, 2))
}

// Messages the handlers hand over as plain English text (ApiErrorMsg) must be
// rendered like any other console message and carry their key, so the web
// console can show them in the language the reader switched to.
func TestApiErrorMsgFollowsReaderLanguage(t *testing.T) {
	require.NoError(t, Init())
	for _, tc := range []struct {
		name   string
		msg    string
		header string
		want   string
	}{
		{
			name:   "known text zh",
			msg:    "no permission",
			header: "zh-CN",
			want:   `{"success":false,"message":"无权限","message_key":"no permission"}`,
		},
		{
			name:   "known text en",
			msg:    "no permission",
			header: "en",
			want:   `{"success":false,"message":"no permission","message_key":"no permission"}`,
		},
		{
			name:   "dynamic detail keeps its wording",
			msg:    "upstream said: 502 bad gateway",
			header: "zh-CN",
			want:   `{"success":false,"message":"upstream said: 502 bad gateway"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/api/channel", nil)
			c.Request.Header.Set(ConsoleLanguageHeader, tc.header)
			common.ApiErrorMsg(c, tc.msg)
			assert.JSONEq(t, tc.want, recorder.Body.String())
		})
	}
}

// The console's current interface language outranks the language saved in the
// account: switching the page must switch the hints immediately.
func TestConsoleLanguageHeaderOutranksUserSetting(t *testing.T) {
	require.NoError(t, Init())
	newContext := func(path string) (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("GET", path, nil)
		c.Request.Header.Set("Accept-Language", "zh-CN")
		common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{Language: "zhCN"})
		return c, recorder
	}

	c, _ := newContext("/api/channel")
	c.Request.Header.Set(ConsoleLanguageHeader, "en")
	assert.Equal(t, LangEn, StatedLang(c))

	c, _ = newContext("/api/channel")
	c.Request.Header.Set(ConsoleLanguageHeader, "zhTW")
	assert.Equal(t, LangZhTW, StatedLang(c))

	// Without the header the saved preference still decides.
	c, _ = newContext("/api/channel")
	assert.Equal(t, LangZhCN, StatedLang(c))

	// Protocol paths ignore the console header: OpenAI-compatible clients get
	// their errors in the language they asked for, not the one a browser claims.
	c, _ = newContext("/v1/chat/completions")
	c.Request.Header.Set(ConsoleLanguageHeader, "en")
	assert.Equal(t, LangZhCN, StatedLang(c))
}

// Mail follows the account preference, never the page the operator happens to
// be looking at.
func TestStatedPreferenceLangIgnoresConsoleHeader(t *testing.T) {
	require.NoError(t, Init())
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/user/login", nil)
	c.Request.Header.Set(ConsoleLanguageHeader, "en")
	common.SetContextKey(c, constant.ContextKeyUserSetting, dto.UserSetting{Language: "zhCN"})
	assert.Equal(t, LangZhCN, StatedPreferenceLang(c))
}
