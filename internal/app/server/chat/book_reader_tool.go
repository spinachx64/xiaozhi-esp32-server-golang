package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "xiaozhi-esp32-server-golang/logger"

	"github.com/spf13/viper"
)

// Book reader prototype: chapters are pre-synthesized audio files listed in
// book_reader.catalog. play_chapter streams one through the session media
// player, so pause/resume/stop go through control_music_playback.

const (
	playChapterToolName  = "play_chapter"
	listChaptersToolName = "list_chapters"
)

type bookChapter struct {
	Book    string `mapstructure:"book" json:"book"`
	Chapter int    `mapstructure:"chapter" json:"chapter"`
	Title   string `mapstructure:"title" json:"title"`
	Path    string `mapstructure:"path" json:"-"`
}

type playChapterParams struct {
	Book    string `json:"book,omitempty" description:"书名，可以只说一部分；不填则播放最新更新的那一章"`
	Chapter int    `json:"chapter,omitempty" description:"第几章；不填则播放这本书最新的一章"`
}

func init() {
	if err := RegisterLocalMcpFunc(playChapterToolName,
		"当用户要听书、朗读章节、播放某本书刚更新的章节时必须调用此工具，只有调用了才会真的开始播放，不能只回复“开播”。"+
			"用户没说书名时直接调用、不填参数，会播放最新一章，不需要先查目录。工具会直接开始播放，调用后不要再说话。"+
			"暂停、继续、停止请用 control_music_playback。",
		playChapterParams{}, playChapterHandler); err != nil {
		log.Errorf("注册 %s 失败: %v", playChapterToolName, err)
	}
	if err := RegisterLocalMcpFunc(listChaptersToolName,
		"当用户问有哪些书可以听、某本书更新到第几章时使用，返回可播放的章节列表",
		struct{}{}, listChaptersHandler); err != nil {
		log.Errorf("注册 %s 失败: %v", listChaptersToolName, err)
	}
}

func loadBookCatalog() []bookChapter {
	var chapters []bookChapter
	if err := viper.UnmarshalKey("book_reader.catalog", &chapters); err != nil {
		log.Warnf("读取 book_reader.catalog 失败: %v", err)
		return nil
	}
	return chapters
}

// findChapter matches the book by substring and picks the requested chapter,
// or the highest chapter number when none is given. The catalog order breaks
// ties, so the last entry counts as the newest update.
func findChapter(chapters []bookChapter, book string, chapter int) (bookChapter, bool) {
	book = strings.TrimSpace(book)
	var best bookChapter
	found := false
	for _, c := range chapters {
		if book != "" && !strings.Contains(c.Book, book) && !strings.Contains(book, c.Book) {
			continue
		}
		if chapter > 0 {
			if c.Chapter == chapter {
				return c, true
			}
			continue
		}
		if book == "" || !found || c.Chapter >= best.Chapter {
			best, found = c, true
		}
	}
	return best, found
}

func chapterMIMEType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	default:
		return "audio/mpeg"
	}
}

func playChapterHandler(ctx context.Context, args string) (string, error) {
	var p playChapterParams
	if strings.TrimSpace(args) != "" {
		_ = json.Unmarshal([]byte(args), &p)
	}
	cm, _ := ctx.Value("chat_session_operator").(*ChatManager)
	if cm == nil {
		return NewErrorResponse(playChapterToolName, "无法确定当前设备", "NO_DEVICE", "请稍后重试").ToJSON()
	}
	session := cm.GetSession()
	if session == nil || session.mediaPlayer == nil {
		return NewErrorResponse(playChapterToolName, "播放器不可用", "NO_PLAYER", "请稍后重试").ToJSON()
	}

	c, ok := findChapter(loadBookCatalog(), p.Book, p.Chapter)
	if !ok {
		return NewErrorResponse(playChapterToolName, "没有找到可播放的章节", "NOT_FOUND",
			"告诉用户没找到这本书或这一章，可以用 list_chapters 看看有哪些").ToJSON()
	}
	if _, err := os.Stat(c.Path); err != nil {
		log.Errorf("章节音频不存在: %s: %v", c.Path, err)
		return NewErrorResponse(playChapterToolName, "章节音频暂时不可用", "AUDIO_MISSING", "告诉用户稍后再试").ToJSON()
	}

	title := fmt.Sprintf("%s 第%d章", c.Book, c.Chapter)
	if c.Title != "" {
		title += " " + c.Title
	}
	chapterLabel := fmt.Sprintf("第%d章", c.Chapter)
	if c.Title != "" {
		chapterLabel += " " + c.Title
	}
	source := MediaSourceDescriptor{
		Title:      title,
		MIMEType:   chapterMIMEType(c.Path),
		SourceType: MediaSourceTypeLocalFile,
		// book/chapter let the device show them on separate lines.
		Meta:  map[string]string{"source": string(MediaSourceTypeLocalFile), "book": c.Book, "chapter": chapterLabel},
		Local: &LocalMediaSource{Path: c.Path},
	}
	log.Infof("设备 %s 开始朗读: %s (%s)", cm.DeviceID, title, c.Path)

	// Play inside the tool call, like SpeakInTurn: the turn's tts stop is
	// sent only after the chapter ends, so the device stays in speaking (mic
	// off, wake word on) for the whole chapter. A wake word cancels ctx and
	// suspends the media; "继续" resumes it via control_music_playback.
	handle, err := session.mediaPlayer.PlaySourceWithHandle(ctx, source)
	if err != nil {
		return NewErrorResponse(playChapterToolName, fmt.Sprintf("播放失败: %v", err), "PLAY_FAILED", "告诉用户稍后再试").ToJSON()
	}
	if err := handle.Wait(ctx); err != nil {
		state := session.mediaPlayer.GetState()
		log.Infof("设备 %s 朗读被打断: %s, position=%dms", cm.DeviceID, title, state.PositionMs)
		return NewActionResponse(playChapterToolName, playChapterToolName,
			"朗读已暂停："+title, "paused", true).ToJSON()
	}
	log.Infof("设备 %s 朗读完成: %s", cm.DeviceID, title)
	return NewActionResponse(playChapterToolName, playChapterToolName,
		"已读完："+title, "finished", true).ToJSON()
}

func listChaptersHandler(ctx context.Context, args string) (string, error) {
	chapters := loadBookCatalog()
	return NewContentResponse(listChaptersToolName, map[string]any{"chapters": chapters},
		fmt.Sprintf("共有 %d 个可播放章节。如果用户想听书，现在就调用 play_chapter 开始播放，不要只说要播；"+
			"如果用户只是问有哪些书，用一两句话告诉他书名和更新到第几章。", len(chapters))).ToJSON()
}
