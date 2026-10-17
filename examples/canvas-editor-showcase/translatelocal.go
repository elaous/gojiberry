//go:build js && wasm

package main

import (
	"context"
	"errors"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/translate"
)

// localTranslator is translate.Translator over the translate add-on (window.addons.translate): the text is translated
// on this device and goes nowhere. The add-on offers
//
//	translate({text, from, to, model}) -> Promise<{text, detected?}>
//
// where from is a language tag or "" (let the add-on find it), to is a language tag ("de", "pt"), and model is the id of
// an installed model (a language pair) or "". Its models (models, install, remove) are listed in the Add-ons tab; a
// model serving a pair has Lang "from>to" (for example "en>de").
type localTranslator struct{}

var _ translate.Translator = localTranslator{}

func translateAddon() bool { return hasAddon("translate", "translate") }

// translator is what the Language tab translates with: the server named in the page's address, else the add-on, else
// nothing.
func (u *ui) translator() translate.Translator {
	if u.lang.translator != nil {
		return u.lang.translator
	}
	if translateAddon() {
		return localTranslator{}
	}
	return nil
}

// pairModel is the installed model for translating from one language into another ("" when none is listed): one whose
// pair is exactly from>to, else any into the target.
func pairModel(from, to string) string {
	to = strings.ToLower(strings.SplitN(to, "-", 2)[0])
	from = strings.ToLower(strings.SplitN(from, "-", 2)[0])
	best := ""
	for _, m := range addonsState.models["translate"] {
		if !m.Installed {
			continue
		}
		src, dst, ok := strings.Cut(strings.ToLower(m.Lang), ">")
		if !ok || strings.SplitN(dst, "-", 2)[0] != to {
			continue
		}
		if from != "" && strings.SplitN(src, "-", 2)[0] == from {
			return m.ID
		}
		if best == "" {
			best = m.ID
		}
	}
	return best
}

// Translate implements translate.Translator. target is a DeepL language code ("DE", "EN-US", "PT-PT"); the add-on gets
// the plain tag ("de", "en", "pt").
func (localTranslator) Translate(ctx context.Context, text, target, source string) (translate.Result, error) {
	if strings.TrimSpace(text) == "" {
		return translate.Result{}, nil
	}
	if !translateAddon() {
		return translate.Result{}, errors.New("the page has no translate add-on")
	}
	to := strings.ToLower(strings.SplitN(target, "-", 2)[0])
	from := strings.ToLower(strings.SplitN(source, "-", 2)[0])
	type result struct {
		v   js.Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := domrt.Await(addon("translate").Call("translate", map[string]any{"text": text, "from": from, "to": to, "model": pairModel(from, to)}))
		done <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		return translate.Result{}, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return translate.Result{}, r.err
		}
		t := r.v.Get("text")
		if t.Type() != js.TypeString {
			return translate.Result{}, errors.New("the translate add-on gave no text")
		}
		out := translate.Result{Text: t.String()}
		if d := r.v.Get("detected"); d.Type() == js.TypeString {
			out.Detected = d.String()
		}
		return out, nil
	}
}
