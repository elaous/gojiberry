//go:build js && wasm

package main

import (
	"context"
	"errors"
	"sync"

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
}

func (f *folderState) get() (state, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == "" {
		return components.FolderNone, ""
	}
	return f.state, f.name
}

// use makes the folder a target of the saver and selects it.
func (u *ui) useFolder(ctx context.Context, a *docstore.FileSystemAdapter) error {
	u.folder.mu.Lock()
	u.folder.state, u.folder.name, u.folder.adapter = components.FolderReady, a.Name(), a
	u.folder.mu.Unlock()
	u.saver.Saver.AddTarget(saver.Target{Name: "folder", Label: "Folder: " + a.Name(), Adapter: a})
	return u.saver.SetTarget(ctx, "folder")
}

// restoreFolder looks for the folder chosen in an earlier visit: if the browser still allows
// it, it is a target again; if it wants a click, the picker offers "Reconnect folder".
func (u *ui) restoreFolder() {
	if !docstore.FileSystemSupported() {
		return
	}
	go func() {
		ctx := context.Background()
		a, ok, err := docstore.RestoreFolder(ctx, folderKey)
		if err != nil || !ok {
			return
		}
		if a.Permission(ctx) == "granted" {
			u.saver.Saver.AddTarget(saver.Target{Name: "folder", Label: "Folder: " + a.Name(), Adapter: a})
			u.folder.mu.Lock()
			u.folder.state, u.folder.name, u.folder.adapter = components.FolderReady, a.Name(), a
			u.folder.mu.Unlock()
		} else {
			u.folder.mu.Lock()
			u.folder.state, u.folder.name, u.folder.adapter = components.FolderReconnect, a.Name(), a
			u.folder.mu.Unlock()
		}
		if u.saver.OnChange != nil {
			u.saver.OnChange()
		}
	}()
}

func (u *ui) chooseFolder() {
	u.saver.Pending.Run(func(ctx context.Context) error {
		a, err := docstore.PickFolder(ctx)
		if errors.Is(err, docstore.ErrPickCancelled) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := a.Remember(ctx, folderKey); err != nil {
			return err
		}
		if err := u.useFolder(ctx, a); err != nil {
			return err
		}
		return u.saver.Refresh(ctx)
	})
}

func (u *ui) reconnectFolder() {
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
