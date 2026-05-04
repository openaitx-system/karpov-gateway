package modules

import (
	"compress/zlib"
	"context"
	"encoding/hex"
	"fmt"
	"io"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/algorithms/tripledes"
)

var qrcKey = []byte("!@#)(*$%123ZXC!@!@#)(NHL")

type GetLyricOptions struct {
	QRC   bool
	Trans bool
	Roma  bool
}

// GetLyric 获取歌词并自动解密，返回 lrc / qrc / trans / roma 四个字段。
func GetLyric(ctx context.Context, c *qqmusic.Client, value any, opts GetLyricOptions) (map[string]any, error) {
	// 第一次：获取普通 LRC + trans + roma（不带 qrc）
	lrcData, err := fetchLyric(ctx, c, value, false, opts.Trans, opts.Roma)
	if err != nil {
		return nil, err
	}

	result := map[string]any{
		"lyric": decryptField(lrcData, "lyric"),
		"trans": decryptField(lrcData, "trans"),
		"roma":  decryptField(lrcData, "roma"),
		"qrc":   "",
	}

	// 第二次：获取 QRC 逐字歌词
	if opts.QRC {
		qrcData, err := fetchLyric(ctx, c, value, true, false, false)
		if err == nil {
			result["qrc"] = decryptField(qrcData, "lyric")
		}
	}

	return result, nil
}

func fetchLyric(ctx context.Context, c *qqmusic.Client, value any, qrc, trans, roma bool) (map[string]any, error) {
	param := map[string]any{
		"crypt":   1,
		"lrc_t":   0,
		"qrc":     qrc,
		"qrc_t":   0,
		"roma":    roma,
		"roma_t":  0,
		"trans":   trans,
		"trans_t": 0,
		"type":    1,
	}
	mergeMap(param, queryCommon(c))
	switch v := value.(type) {
	case int:
		param["songId"] = v
	case int64:
		param["songId"] = v
	default:
		param["songMid"] = v
	}
	return callJSON(ctx, c, "music.musichallSong.PlayLyricInfo", "GetPlayLyricInfo", param, qqmusic.MusicuOptions{})
}

func decryptField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	crypt, _ := data["crypt"].(float64)
	val, _ := data[key].(string)
	if val == "" {
		return ""
	}
	if int(crypt) == 1 {
		return qrcDecrypt(val)
	}
	return val
}

// qrcDecrypt 解密 QRC/歌词：hex decode → 3DES EDE decrypt → zlib decompress。
func qrcDecrypt(encrypted string) string {
	if encrypted == "" {
		return ""
	}
	raw, err := hex.DecodeString(encrypted)
	if err != nil {
		return ""
	}
	sched, err := tripledes.KeySetup(qrcKey, tripledes.Decrypt)
	if err != nil {
		return ""
	}
	if len(raw)%8 != 0 {
		return ""
	}
	decrypted := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i += 8 {
		var block [8]byte
		copy(block[:], raw[i:i+8])
		out := tripledes.Crypt(block, sched)
		decrypted = append(decrypted, out[:]...)
	}
	r, err := zlib.NewReader(ioBytes(decrypted))
	if err != nil {
		return ""
	}
	defer r.Close()
	result, err := io.ReadAll(r)
	if err != nil {
		return fmt.Sprintf("[decompress error: %v]", err)
	}
	return string(result)
}

type bytesReader struct{ b []byte; i int }
func ioBytes(b []byte) io.Reader { return &bytesReader{b: b} }
func (r *bytesReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) { return 0, io.EOF }
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}
