package speechcoach

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

type Recogniser struct {
	URL   string // the /transcribe address
	Token string // optional bearer token the person supplies
	Doer  Doer   // nil: http.DefaultClient
}

// Transcriber turns a take into text.
type Transcriber interface {
	Transcribe(ctx context.Context, pcm []int16, prompt, lang string) (string, error)
}

var _ Transcriber = (*Recogniser)(nil)

// MaxReply limits what is read back.
const MaxReply = 1 << 20

// Transcribe sends the take and returns the recognised text.
func (r *Recogniser) Transcribe(ctx context.Context, pcm []int16, prompt, lang string) (string, error) {
	if strings.TrimSpace(r.URL) == "" {
		return "", ErrNoEndpoint
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("audio", "take.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(EncodeWAV(pcm)); err != nil {
		return "", err
	}
	if prompt != "" {
		_ = mw.WriteField("prompt", prompt)
	}
	if lang != "" {
		_ = mw.WriteField("language", lang)
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.URL, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	d := r.Doer
	if d == nil {
		d = http.DefaultClient
	}
	res, err := d.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the recogniser answered %s", http.StatusText(res.StatusCode))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, MaxReply)).Decode(&out); err != nil {
		return "", fmt.Errorf("the recogniser's answer could not be read")
	}
	return strings.TrimSpace(out.Text), nil
}
