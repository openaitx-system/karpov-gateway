package modules

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
)

// captureCall 是一个能捕获请求体的 mock：返回固定 req_0.data={"ok":1}。
func captureCall(t *testing.T) (*qqmusic.Client, *map[string]any) {
	t.Helper()
	captured := &map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		_ = json.Unmarshal(body, &p)
		*captured = p
		_, _ = w.Write([]byte(`{"req_0":{"code":0,"data":{"ok":1}}}`))
	}))
	t.Cleanup(srv.Close)
	httpc := qqmusic.NewHTTPClient(qqmusic.HTTPClientOptions{
		MaxRetries: 0, BackoffBase: 5 * time.Millisecond, MaxConnections: 4,
		Timeout: 2 * time.Second, HTTP2: false,
	})
	c := qqmusic.NewClient(qqmusic.ClientOptions{
		HTTP: httpc, Platform: qqmusic.PlatformAndroid, MaxConcurrency: 4,
		Credential: &qqmusic.Credential{StrMusicID: "999"},
		MusicuURL:  srv.URL,
	})
	return c, captured
}

func mustReq0(t *testing.T, p map[string]any) (string, string, map[string]any) {
	t.Helper()
	r0, ok := p["req_0"].(map[string]any)
	if !ok {
		t.Fatalf("missing req_0: %v", p)
	}
	param, _ := r0["param"].(map[string]any)
	return r0["module"].(string), r0["method"].(string), param
}

func TestGetAlbumDetail_ByID(t *testing.T) {
	c, p := captureCall(t)
	if _, err := GetAlbumDetail(context.Background(), c, 100); err != nil {
		t.Fatalf("err: %v", err)
	}
	mod, mth, param := mustReq0(t, *p)
	if mod != "music.musichallAlbum.AlbumInfoServer" || mth != "GetAlbumDetail" {
		t.Errorf("module/method: %s/%s", mod, mth)
	}
	if param["albumId"] != float64(100) {
		t.Errorf("albumId: %v", param["albumId"])
	}
}

func TestGetAlbumDetail_ByMID(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetAlbumDetail(context.Background(), c, "ABC")
	_, _, param := mustReq0(t, *p)
	if param["albumMId"] != "ABC" {
		t.Errorf("albumMId: %v", param["albumMId"])
	}
}

func TestGetAlbumSongList_Pagination(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetAlbumSongList(context.Background(), c, "ABC", 20, 3)
	_, _, param := mustReq0(t, *p)
	// begin = num*(page-1) = 40
	if param["begin"] != float64(40) || param["num"] != float64(20) {
		t.Errorf("paging: %+v", param)
	}
}

func TestGetSingerDetail(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetSingerDetail(context.Background(), c, []string{"M1", "M2"})
	mod, mth, param := mustReq0(t, *p)
	if mod != "music.musichallSinger.SingerInfoInter" || mth != "GetSingerDetail" {
		t.Errorf("%s/%s", mod, mth)
	}
	mids, ok := param["singer_mids"].([]any)
	if !ok || len(mids) != 2 {
		t.Errorf("singer_mids: %+v", param["singer_mids"])
	}
}

func TestGetSonglistDetail(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetSonglistDetail(context.Background(), c, 12345, 10, 1)
	mod, mth, param := mustReq0(t, *p)
	if mod != "music.srfDissInfo.DissInfo" || mth != "CgiGetDiss" {
		t.Errorf("%s/%s", mod, mth)
	}
	if param["disstid"] != float64(12345) {
		t.Errorf("disstid: %v", param["disstid"])
	}
}

func TestGetLyric(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetLyric(context.Background(), c, "MID1", GetLyricOptions{QRC: true, Trans: true})
	mod, mth, param := mustReq0(t, *p)
	if mod != "music.musichallSong.PlayLyricInfo" || mth != "GetPlayLyricInfo" {
		t.Errorf("%s/%s", mod, mth)
	}
	if param["songMid"] != "MID1" {
		t.Errorf("songMid: %v", param["songMid"])
	}
	// bool→0/1 转换后应为 1
	if param["qrc"] != float64(1) || param["trans"] != float64(1) {
		t.Errorf("bool flags: qrc=%v trans=%v", param["qrc"], param["trans"])
	}
	// queryCommon 注入了 ct/cv
	if _, ok := param["ct"]; !ok {
		t.Errorf("missing ct: %+v", param)
	}
}

func TestGetMVDetail(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetMVDetail(context.Background(), c, []string{"V1"})
	mod, mth, _ := mustReq0(t, *p)
	if mod != "video.VideoDataServer" || mth != "get_video_info_batch" {
		t.Errorf("%s/%s", mod, mth)
	}
}

func TestGetTopAll(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetTopAll(context.Background(), c)
	mod, mth, _ := mustReq0(t, *p)
	if mod != "music.musicToplist.Toplist" || mth != "GetAll" {
		t.Errorf("%s/%s", mod, mth)
	}
}

func TestGetSongDetail_WebPlatform(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetSongDetail(context.Background(), c, "MID1")
	// Web platform 时 comm.platform = "yqq.json"
	comm := (*p)["comm"].(map[string]any)
	if comm["platform"] != "yqq.json" {
		t.Errorf("platform should be web yqq.json; comm=%v", comm)
	}
}

func TestGetSongURLs(t *testing.T) {
	c, p := captureCall(t)
	_, _ = GetSongURLs(context.Background(), c, []string{"MID1", "MID2"}, "M500", ".mp3", "GUID")
	mod, mth, param := mustReq0(t, *p)
	if mod != "music.vkey.GetVkey" || mth != "UrlGetVkey" {
		t.Errorf("%s/%s", mod, mth)
	}
	files := param["filename"].([]any)
	if len(files) != 2 || files[0] != "M500MID1MID1.mp3" {
		t.Errorf("filename: %+v", files)
	}
	if param["uin"] != "999" {
		t.Errorf("uin: %v", param["uin"])
	}
}
