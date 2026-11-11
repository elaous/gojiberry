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

const folderKey = "showcase"

type folderState struct {
	mu      sync.Mutex
	state   string // components.FolderNone, FolderReady or FolderReconnect
	name    string
	adapter *docstore.FileSystemAdapter
	gen     int
}

func (f *folderState) get() (state, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return components.FolderNone, ""
	}
	return f.state, f.name
}

func (f *folderState) bump() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	return f.gen
}

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

func (u *ui) folderLapsed(a *docstore.FileSystemAdapter) {
	u.folder.mu.Lock()
	if u.folder.adapter != a || u.folder.state != components.FolderReady {
		u.folder.mu.Unlock()
		return
	}
	u.folder.state = components.FolderReconnect
	u.folder.gen++
	u.folder.mu.Unlock()
	u.saver.Pending.Run(func(ctx context.Context) error {
		var err error
		if t, _ := u.saver.Where(); t == "folder" {
			err = u.saver.SetTarget(ctx, "browser")
		}
		u.saver.Saver.RemoveTarget("folder")
		return err
	})
}

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
		var setErr error
		if t, _ := u.saver.Where(); t == "folder" {
			setErr = u.saver.SetTarget(ctx, "browser")
		}
		u.saver.Saver.RemoveTarget("folder")
		_ = docstore.ForgetFolder(ctx, folderKey)
		u.folder.mu.Lock()
		u.folder.state, u.folder.name, u.folder.adapter = components.FolderNone, "", nil
		u.folder.mu.Unlock()
		if err := u.saver.Refresh(ctx); err != nil && setErr == nil {
			return err
		}
		return setErr
	})
}

func (u *ui) folderPanel() xango.Node {
	state, name := u.folder.get()
	return components.FolderPicker(components.FolderPickerProps{
		Supported: docstore.FileSystemSupported(), State: state, Name: name,
		OnChoose: u.chooseFolder, OnReconnect: u.reconnectFolder, OnForget: u.forgetFolder,
		Disabled: u.saver.Pending.Busy(),
	})
}
