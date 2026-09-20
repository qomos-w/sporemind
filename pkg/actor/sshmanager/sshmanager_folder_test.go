package sshmanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestFolderCreatePersistsEmptyFolder(t *testing.T) {
	a := newTestActor(t)

	if _, err := a.handleFolderCreate(adminCtx(), domain.SshFolderCreateReq{Name: "prod"}); err != nil {
		t.Fatalf("folder create: %v", err)
	}
	list, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	if len(list.Groups) != 1 || list.Groups[0] != "prod" {
		t.Fatalf("expected [prod], got %+v", list.Groups)
	}

	if _, err := a.handleFolderCreate(adminCtx(), domain.SshFolderCreateReq{Name: "prod"}); err == nil {
		t.Fatal("duplicate folder create should error")
	}
	if _, err := a.handleFolderCreate(adminCtx(), domain.SshFolderCreateReq{Name: "  "}); err == nil {
		t.Fatal("blank folder name should error")
	}
}

func TestFolderListMergesHostDerivedGroups(t *testing.T) {
	a := newTestActor(t)
	a.Hosts = []domain.SshHost{
		{ID: "1", Name: "a", Group: "zeta"},
		{ID: "2", Name: "b", Group: "alpha"},
	}
	a.Groups = []string{"prod"}

	list, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	want := []string{"prod", "alpha", "zeta"}
	if len(list.Groups) != len(want) {
		t.Fatalf("expected %+v, got %+v", want, list.Groups)
	}
	for i := range want {
		if list.Groups[i] != want[i] {
			t.Fatalf("expected %+v, got %+v", want, list.Groups)
		}
	}
}

func TestFolderRenameUpdatesHosts(t *testing.T) {
	a := newTestActor(t)
	a.Hosts = []domain.SshHost{{ID: "1", Name: "a", Group: "old"}}
	a.Groups = []string{"old", "other"}

	if _, err := a.handleFolderRename(adminCtx(), domain.SshFolderRenameReq{From: "old", To: "new"}); err != nil {
		t.Fatalf("folder rename: %v", err)
	}
	if a.Hosts[0].Group != "new" {
		t.Fatalf("host group not renamed: %q", a.Hosts[0].Group)
	}
	if a.Groups[0] != "new" {
		t.Fatalf("folder order not renamed: %+v", a.Groups)
	}

	// Rename of a host-derived group (not in Groups) still works.
	if _, err := a.handleFolderRename(adminCtx(), domain.SshFolderRenameReq{From: "new", To: "newer"}); err != nil {
		t.Fatalf("folder rename: %v", err)
	}
	if a.Hosts[0].Group != "newer" {
		t.Fatalf("host group not renamed: %q", a.Hosts[0].Group)
	}

	if _, err := a.handleFolderRename(adminCtx(), domain.SshFolderRenameReq{From: "missing", To: "x"}); err == nil {
		t.Fatal("rename of missing folder should error")
	}
}

func TestFolderReorder(t *testing.T) {
	a := newTestActor(t)
	a.Groups = []string{"a", "b", "c"}

	if _, err := a.handleFolderReorder(adminCtx(), domain.SshFolderReorderReq{Names: []string{"c", "a", "b"}}); err != nil {
		t.Fatalf("folder reorder: %v", err)
	}
	list, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	want := []string{"c", "a", "b"}
	for i := range want {
		if list.Groups[i] != want[i] {
			t.Fatalf("expected %+v, got %+v", want, list.Groups)
		}
	}
}

func TestFolderRemoveUngroupsHosts(t *testing.T) {
	a := newTestActor(t)
	a.Hosts = []domain.SshHost{{ID: "1", Name: "a", Group: "gone"}}
	a.Groups = []string{"gone", "keep"}

	if _, err := a.handleFolderRemove(adminCtx(), domain.SshFolderRemoveReq{Name: "gone"}); err != nil {
		t.Fatalf("folder remove: %v", err)
	}
	if a.Hosts[0].Group != "" {
		t.Fatalf("host should be ungrouped, got %q", a.Hosts[0].Group)
	}
	list, err := a.handleHostList(adminCtx(), domain.SshHostListReq{})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	if len(list.Groups) != 1 || list.Groups[0] != "keep" {
		t.Fatalf("expected [keep], got %+v", list.Groups)
	}
}

func TestFolderStateSurvivesSaveLoad(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleFolderCreate(adminCtx(), domain.SshFolderCreateReq{Name: "prod"}); err != nil {
		t.Fatalf("folder create: %v", err)
	}

	b := &Actor{store: a.store, Credentials: make(map[string]sshCredential)}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(b.Groups) != 1 || b.Groups[0] != "prod" {
		t.Fatalf("groups not persisted: %+v", b.Groups)
	}
}
