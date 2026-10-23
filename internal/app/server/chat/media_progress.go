package chat

import (
	"encoding/json"
	"os"
	"strings"

	log "xiaozhi-esp32-server-golang/logger"

	"github.com/hajimehoshi/go-mp3"
)

// Media progress messages let a device show a progress ring for long media
// such as book chapters. They are sent only on state changes; the device
// extrapolates the position with its own clock in between:
//
//	{"type":"media","state":"start","title":"...","position_ms":0,"duration_ms":881840}
//	{"type":"media","state":"pause","position_ms":149040}
//	{"type":"media","state":"resume","position_ms":149040}
//	{"type":"media","state":"stop"}
//
// position_ms counts frames handed to the transport, so it runs slightly
// ahead of what the device has played (about one buffer). Devices that do not
// know the message ignore it.
const (
	mediaProgressStart  = "start"
	mediaProgressPause  = "pause"
	mediaProgressResume = "resume"
	mediaProgressStop   = "stop"
)

type mediaProgressMessage struct {
	Type       string `json:"type"`
	SessionID  string `json:"session_id,omitempty"`
	State      string `json:"state"`
	Title      string `json:"title,omitempty"`
	Book       string `json:"book,omitempty"`    // book chapters only: shown on separate lines
	Chapter    string `json:"chapter,omitempty"` // e.g. "第1章 第一章"
	PositionMs int64  `json:"position_ms"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

func sendMediaProgress(attachment *mediaSessionAttachment, msg mediaProgressMessage) {
	if attachment == nil || attachment.session == nil || attachment.session.serverTransport == nil {
		return
	}
	msg.Type = "media"
	if attachment.session.clientState != nil {
		msg.SessionID = attachment.session.clientState.SessionID
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := attachment.session.serverTransport.SendCmd(b); err != nil {
		log.Debugf("发送媒体进度失败: state=%s err=%v", msg.State, err)
	}
}

// sendCurrentMediaProgress sends a position-less state (stop) to the device
// the runtime is attached to now.
func (r *deviceMediaRuntime) sendCurrentMediaProgress(state string) {
	r.mu.RLock()
	attachment := r.attachment
	r.mu.RUnlock()
	sendMediaProgress(attachment, mediaProgressMessage{State: state})
}

// probeMediaDurationMs returns the length of a local MP3 file, or 0 when it is
// unknown (other formats, remote sources). It only scans frame headers.
func probeMediaDurationMs(source MediaSourceDescriptor) int64 {
	if source.SourceType != MediaSourceTypeLocalFile || source.Local == nil {
		return 0
	}
	if mt := strings.ToLower(source.MIMEType); mt != "" && !strings.Contains(mt, "mpeg") && !strings.Contains(mt, "mp3") {
		return 0
	}
	f, err := os.Open(source.Local.Path)
	if err != nil {
		return 0
	}
	defer f.Close()
	d, err := mp3.NewDecoder(f)
	if err != nil || d.SampleRate() <= 0 {
		return 0
	}
	// Length is in bytes of 16-bit stereo PCM: 4 bytes per sample frame.
	return d.Length() / 4 * 1000 / int64(d.SampleRate())
}
