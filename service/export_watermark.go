package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 导出文件水印（v1.1.0 M1 · B143）。
//
// 导出是把库里数据搬到站点之外的唯一常规通道：管理员能导出全部用户的日志，
// 普通用户能导出自己的用量。文件一旦落盘就会被转发、截图、贴进工单，事后
// 无法从内容本身分辨是谁导出的。这里在每个导出文件里写入一条带 HMAC 签名的
// 水印记录，记录操作者、时间、范围与过滤条件：
//
//   - 水印是文件内容的一部分（NDJSON 首行 / CSV 末尾行），跟着文件一起被复制；
//   - 签名用服务端密钥派生，泄漏者删掉水印或改写操作者后无法重新签名，因此
//     「没有水印」或「签名对不上」本身就能说明文件被动过；
//   - 校验不需要原始请求：拿到文件的人把首行交回运营方，用 Verify 即可确认
//     该文件由谁在什么时候导出。
//
// 已知边界：水印能证明「这份文件出自本系统的某次导出、导出者是谁」，不能阻止
// 有人把内容复制到新文件里去掉水印（那属于内容泄漏，由审计日志 B78 的哈希链
// 与导出审计记录交叉比对）。

// ExportWatermarkVersion lets a future change of the payload layout stay
// distinguishable from files exported today.
const ExportWatermarkVersion = 1

// Export scopes, kept short because they end up inside the signed payload.
const (
	ExportScopeAll  = "all"
	ExportScopeSelf = "self"
)

var ErrExportWatermarkSignature = errors.New("export watermark: signature does not match the payload")

// ExportWatermark is the record written into an exported file. Field order and
// names are part of the file format consumed by support tooling, so treat them
// as stable.
type ExportWatermark struct {
	Version    int            `json:"version"`
	ExporterId int            `json:"exporter_id"`
	Username   string         `json:"username"`
	Role       int            `json:"role"`
	ExportedAt int64          `json:"exported_at"`
	Scope      string         `json:"scope"`
	Filter     map[string]any `json:"filter,omitempty"`
	Nonce      string         `json:"nonce"`
	Signature  string         `json:"signature"`
}

// NewExportWatermark builds and signs a watermark for one export. filter holds
// the search that produced the file, so two exports by the same operator at the
// same second still differ.
func NewExportWatermark(exporterId int, username string, role int, scope string, filter map[string]any) (*ExportWatermark, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	watermark := &ExportWatermark{
		Version:    ExportWatermarkVersion,
		ExporterId: exporterId,
		Username:   username,
		Role:       role,
		ExportedAt: common.GetTimestamp(),
		Scope:      scope,
		Filter:     filter,
		Nonce:      hex.EncodeToString(nonce),
	}
	watermark.Sign()
	return watermark, nil
}

// exportWatermarkKey derives the signing key from CryptoSecret. The derivation
// is salted so this HMAC never shares key material with the secret cipher or
// the session signer.
func exportWatermarkKey() []byte {
	digest := sha256.Sum256([]byte("new-api:export-watermark:v1:" + common.CryptoSecret))
	return digest[:]
}

// canonicalPayload is the exact byte sequence covered by the signature. Every
// variable-length field is length-prefixed so no two different watermarks can
// collide by shifting bytes across field boundaries.
func (w *ExportWatermark) canonicalPayload() string {
	// json.Marshal sorts map keys, so the filter renders identically on the
	// signing and the verifying side without extra sorting here.
	filter, err := json.Marshal(w.Filter)
	if err != nil {
		filter = []byte("{}")
	}
	return fmt.Sprintf("version=%d\nexporter=%d\nusername=%d:%s\nrole=%d\nat=%d\nscope=%d:%s\nfilter=%d:%s\nnonce=%d:%s",
		w.Version,
		w.ExporterId,
		len(w.Username), w.Username,
		w.Role,
		w.ExportedAt,
		len(w.Scope), w.Scope,
		len(filter), filter,
		len(w.Nonce), w.Nonce,
	)
}

// Sign fills Signature. Callers that mutate a field must sign again.
func (w *ExportWatermark) Sign() {
	mac := hmac.New(sha256.New, exportWatermarkKey())
	mac.Write([]byte(w.canonicalPayload()))
	w.Signature = hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether the stored signature still matches the payload, i.e.
// whether this watermark was produced by this deployment and has not been
// edited since.
func (w *ExportWatermark) Verify() error {
	if w == nil {
		return ErrExportWatermarkSignature
	}
	if w.Version != ExportWatermarkVersion {
		return fmt.Errorf("export watermark: unsupported version %d", w.Version)
	}
	expected := *w
	expected.Sign()
	// Constant-time compare: the signature is not a secret, but keeping the
	// check uniform avoids handing a timestamp oracle to anyone probing files.
	if !hmac.Equal([]byte(expected.Signature), []byte(w.Signature)) {
		return ErrExportWatermarkSignature
	}
	return nil
}

// ExportWatermarkLine renders the single NDJSON line that opens an export
// stream. It is wrapped in an object so a JSON line reader can tell the
// watermark apart from a log row without inspecting every field.
func ExportWatermarkLine(w *ExportWatermark) ([]byte, error) {
	return common.Marshal(map[string]any{"export_watermark": w})
}

// DescribeExportWatermark renders the human-readable summary used by support
// tooling and by the CSV footer.
func DescribeExportWatermark(w *ExportWatermark) string {
	if w == nil {
		return ""
	}
	scope := "own logs"
	if w.Scope == ExportScopeAll {
		scope = "all users"
	}
	return fmt.Sprintf("%s (#%d) exported %s at %s (UTC) · watermark %s",
		w.Username, w.ExporterId, scope, formatWatermarkTime(w.ExportedAt), w.Nonce)
}

func formatWatermarkTime(timestamp int64) string {
	return time.Unix(timestamp, 0).UTC().Format("2006-01-02 15:04:05")
}
