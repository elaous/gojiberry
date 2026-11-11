//go:build js && wasm

package main

import (
	"context"
	"errors"
	"syscall/js"

	"github.com/elaous/gojiberry/addons"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/spell"
)

const spellAddonPrefix = "addon:"

func spellAddon() bool { return hasAddon("spell", "load") }

var spellAddonAsked bool

func askSpellAddon(renderUI func()) {
	if spellAddonAsked || !spellAddon() || !hasAddon("spell", "models") {
		return
	}
	spellAddonAsked = true
	addonModels("spell", func(list []addons.Model, err error) {
		if err == nil {
			if addonsState.models == nil {
				addonsState.models, addonsState.loading, addonsState.errs = map[string][]addons.Model{}, map[string]bool{}, map[string]string{}
			}
			addonsState.models["spell"] = append([]addons.Model{}, list...)
		}
		renderUI()
	})
}

func loadAddonDictionary(ctx context.Context, id string) (*spell.Dictionary, error) {
	if !spellAddon() {
		return nil, errors.New("the page has no spell add-on")
	}
	type result struct {
		v   js.Value
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := domrt.Await(addon("spell").Call("load", id))
		done <- result{v, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		dic, aff := r.v.Get("dic"), r.v.Get("aff")
		if !dic.Truthy() || !aff.Truthy() {
			return nil, errors.New("the spell add-on gave no dictionary files")
		}
		return spell.LoadHunspell(bytesOf(dic), bytesOf(aff), spell.HunspellOptions{}), nil
	}
}
