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

// The spell add-on (window.addons.spell) keeps Hunspell dictionaries on the device so that spelling works offline in
// more languages than the one built in. It does not check anything itself: it hands over the files, and the page reads
// them (spell.LoadHunspell). It offers the usual models(), install(id, onProgress) and remove(id), and
//
//	load(id) -> Promise<{dic: Uint8Array, aff: Uint8Array}>
//
// where dic and aff are the bytes of the dictionary's .dic and .aff files.

// spellAddonPrefix starts the name of a dictionary that comes from the add-on, in the Spelling tab's list.
const spellAddonPrefix = "addon:"

func spellAddon() bool { return hasAddon("spell", "load") }

// spellAddonAsked is set once the page has listed the add-on's dictionaries.
var spellAddonAsked bool

// askSpellAddon lists the add-on's dictionaries (once), so that the installed ones are offered next to the others. Listing
// asks nothing of the network: it is the add-on's own record of what it holds.
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

// loadAddonDictionary asks the add-on for the files of dictionary id and reads them. It blocks: call it from a goroutine.
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
