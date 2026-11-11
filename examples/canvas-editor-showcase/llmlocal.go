//go:build js && wasm

package main

import (
	"context"
	"errors"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/grammar"
)

func llmAddon() bool { return hasAddon("llm", "generate") }

func generate(ctx context.Context, prompt, format string, maxTokens int) (string, error) {
	if !llmAddon() {
		return "", errors.New("the page has no llm add-on")
	}
	type result struct {
		v   js.Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := domrt.Await(addon("llm").Call("generate", map[string]any{"prompt": prompt, "format": format, "maxTokens": maxTokens, "model": installedModel("llm", "")}))
		done <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		if r.err != nil {
			return "", r.err
		}
		t := r.v.Get("text")
		if t.Type() != js.TypeString {
			return "", errors.New("the llm add-on gave no text")
		}
		return t.String(), nil
	}
}

type llmGrammar struct{}

var _ grammar.Checker = llmGrammar{}

func (llmGrammar) Check(ctx context.Context, text string) ([]grammar.Issue, error) {
	text = grammar.Cut(text)
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	answer, err := generate(ctx, grammar.Prompt(text), "json", 2048)
	if err != nil {
		return nil, err
	}
	return grammar.ParseIssues(answer, text)
}

func (u *ui) grammarChecker() grammar.Checker {
	if u.lang.grammar != nil {
		return u.lang.grammar
	}
	if llmAddon() {
		return llmGrammar{}
	}
	return nil
}
