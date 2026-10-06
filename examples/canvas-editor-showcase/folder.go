//go:build js && wasm

package main

import (
	"context"
	"errors"
	"sync"
	"syscall/js"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/xango"
)

// folderKey is where the chosen folder's handle is remembered in IndexedDB.
const folderKey = "showcase"

// folderState is the "Folder on this computer" target: the browser lets the page write a folder
// the person chose, and may ask for a click to allow it again after a restart.
type folderState struct {
	mu      sync.Mutex
	state   string // components.FolderNone, FolderReady or FolderReconnect
	name    string
	adapter *docstore.FileSystemAdapter
	gen     int // bumped by every choice the person makes, so a slower restore knows it is stale
}

func (f *folderState) get() (state, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return components.FolderNone, ""
	}
	return f.state, f.name
}

// bump records that the person has decided something about the folder, and returns the new
// generation.
func (f *folderState) bump() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	return f.gen
}

// lapseAdapter is the folder as the saver sees it: when the browser refuses access (the
// permission lapsed mid-session) the folder is taken out of use and the picker offers Reconnect.
type lapseAdapter struct {
	*docstore.FileSystemAdapter
	lapsed func(error)
}

func (l lapseAdapter) check(err error) error {
	if errors.Is(err, docstore.ErrPermission) {
		l.lapsed(err)
	}
	return err
}

func (l lapseAdapter) Save(ctx context.Context, id string, data []byte) error {
	return l.check(l.FileSystemAdapter.Save(ctx, id, data))
}

func (l lapseAdapter) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	return l.check(l.FileSystemAdapter.SaveIf(ctx, id, data, prev))
}

func (l lapseAdapter) Load(ctx context.Context, id string) (docstore.Document, error) {
	d, err := l.FileSystemAdapter.Load(ctx, id)
	return d, l.check(err)
}

func (l lapseAdapter) List(ctx context.Context) ([]docstore.Meta, error) {
	m, err := l.FileSystemAdapter.List(ctx)
	return m, l.check(err)
}

func (l lapseAdapter) Delete(ctx context.Context, id string) error {
	return l.check(l.FileSystemAdapter.Delete(ctx, id))
}

func (u *ui) folderTarget(a *docstore.FileSystemAdapter) saver.Target {
	return saver.Target{Name: "folder", Label: "Folder: " + a.Name(), Adapter: lapseAdapter{a, func(error) { u.folderLapsed(a) }}}
}

// folderLapsed is called when the folder refuses a read or write: it stops being a target
// (the document stays where it is) and the picker shows Reconnect folder.
func (u *ui) folderLapsed(a *docstore.FileSystemAdapter) {
	u.folder.mu.Lock()
	if u.folder.adapter != a || u.folder.state != components.FolderReady {
		u.folder.mu.Unlock()
		return
	}
	u.folder.state = components.FolderReconnect
	u.folder.gen++
	u.folder.mu.Unlock()
	// Not on the goroutine of the operation that failed, which holds the session's lock.
	u.saver.Pending.Run(func(ctx context.Context) error {
		if t, _ := u.saver.Where(); t == "folder" {
			if err := u.saver.SetTarget(ctx, "browser"); err != nil {
				return err
			}
		}
		u.saver.Saver.RemoveTarget("folder")
		return nil
	})
}

// useFolder makes the folder a target of the saver and selects it. The folder is Ready only
// once the saver has accepted it.
func (u *ui) useFolder(ctx context.Context, a *docstore.FileSystemAdapter) error {
	u.saver.Saver.AddTarget(u.folderTarget(a))
	if err := u.saver.SetTarget(ctx, "folder"); err != nil {
		u.saver.Saver.RemoveTarget("folder")
		return err
	}
	u.folder.mu.Lock()
	u.folder.state, u.folder.name, u.folder.adapter = components.FolderReady, a.Name(), a
	u.folder.mu.Unlock()
	return nil
}

const lastTargetKey = "showcase:last-target"

// rememberTarget keeps the target in use, so a later visit can go back to the folder.
func rememberTarget(name string) {
	defer func() { _ = recover() }()
	js.Global().Get("localStorage").Call("setItem", lastTargetKey, name)
}

func lastTarget() (name string) {
	defer func() {
		if recover() != nil {
			name = ""
		}
	}()
	v := js.Global().Get("localStorage").Call("getItem", lastTargetKey)
	if v.IsNull() || v.IsUndefined() {
		return ""
	}
	return v.String()
}

// restoreFolder looks for the folder chosen in an earlier visit: if the browser still allows
// it, it is a target again (and the one in use if it was when the page was last closed); if it
// wants a click, the picker offers "Reconnect folder". If the person chooses, reconnects or
// stops using a folder before this finishes, what it found is dropped.
func (u *ui) restoreFolder() {
	if !docstore.FileSystemSupported() {
		return
	}
	gen := u.folder.bump()
	wasFolder := lastTarget() == "folder"
	go func() {
		ctx := context.Background()
		a, ok, err := docstore.RestoreFolder(ctx, folderKey)
		if err != nil || !ok {
			return
		}
		granted := a.Permission(ctx) == "granted"
		u.folder.mu.Lock()
		if u.folder.gen != gen {
			u.folder.mu.Unlock()
			return
		}
		u.folder.name, u.folder.adapter = a.Name(), a
		if granted {
			u.folder.state = components.FolderReady
			u.saver.Saver.AddTarget(u.folderTarget(a))
		} else {
			u.folder.state = components.FolderReconnect
		}
		u.folder.mu.Unlock()
		if granted && wasFolder {
			u.saver.Pending.Run(func(ctx context.Context) error {
				u.folder.mu.Lock()
				stale := u.folder.gen != gen
				u.folder.mu.Unlock()
				if stale {
					return nil
				}
				return u.saver.SetTarget(ctx, "folder")
			})
		}
		if u.saver.OnChange != nil {
			u.saver.OnChange()
		}
	}()
}

func (u *ui) chooseFolder() {
	u.folder.bump()
	u.saver.Pending.Run(func(ctx context.Context) error {
		a, err := docstore.PickFolder(ctx)
		if errors.Is(err, docstore.ErrPickCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
		u.folder.bump()
		// A folder that cannot be remembered still works for this visit.
		if err := a.Remember(ctx, folderKey); err != nil {
			js.Global().Get("console").Call("warn", "the folder will not be remembered: "+err.Error())
		}
		if err := u.useFolder(ctx, a); err != nil {
			return err
		}
		return u.saver.Refresh(ctx)
	})
}

func (u *ui) reconnectFolder() {
	u.folder.bump()
	u.saver.Pending.Run(func(ctx context.Context) error {
		u.folder.mu.Lock()
		a := u.folder.adapter
		u.folder.mu.Unlock()
		if a == nil {
			return nil
		}
		granted, err := a.Request(ctx)
		if err != nil {
			return err
		}
		if !granted {
			return errors.New("the browser did not allow the folder")
		}
		if err := u.useFolder(ctx, a); err != nil {
			return err
		}
		return u.saver.Refresh(ctx)
	})
}

func (u *ui) forgetFolder() {
	u.folder.bump()
	u.saver.Pending.Run(func(ctx context.Context) error {
		if t, _ := u.saver.Where(); t == "folder" {
			if err := u.saver.SetTarget(ctx, "browser"); err != nil {
				return err
			}
		}
		u.saver.Saver.RemoveTarget("folder")
		_ = docstore.ForgetFolder(ctx, folderKey)
		u.folder.mu.Lock()
		u.folder.state, u.folder.name, u.folder.adapter = components.FolderNone, "", nil
		u.folder.mu.Unlock()
		return u.saver.Refresh(ctx)
	})
}

// folderPanel is the folder control above the save form.
func (u *ui) folderPanel() xango.Node {
	state, name := u.folder.get()
	return components.FolderPicker(components.FolderPickerProps{
		Supported: docstore.FileSystemSupported(), State: state, Name: name,
		OnChoose: u.chooseFolder, OnReconnect: u.reconnectFolder, OnForget: u.forgetFolder,
		Disabled: u.saver.Pending.Busy(),
	})
}
