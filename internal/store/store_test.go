// Copyright 2026 kamenxrider and contributors. Licensed under Apache-2.0. See LICENSE.

package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	return mustOpen(t, filepath.Join(t.TempDir(), "hollis.db"))
}

func mustOpen(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestConversationLifecycle(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	conv, err := st.CreateConversation("cloud", "")
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID == "" {
		t.Fatal("empty conversation id")
	}

	got, err := st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != conv.ID || got.Model != "cloud" {
		t.Fatalf("conversation mismatch: %+v", got)
	}

	if _, err := st.AppendMessage(conv.ID, "user", "Remember VANTA-ORBIT-7319"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendMessage(conv.ID, "assistant", "ACK"); err != nil {
		t.Fatal(err)
	}
	msgs, err := st.Messages(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("message count = %d, want 2", len(msgs))
	}
	if msgs[0].Seq != 0 || msgs[1].Seq != 1 {
		t.Fatalf("seqs = %d,%d want 0,1", msgs[0].Seq, msgs[1].Seq)
	}
	if n, _ := st.MessageCount(conv.ID); n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	if err := st.SetTitle(conv.ID, "Gateway design"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetConversation(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Gateway design" {
		t.Fatalf("title = %q", got.Title)
	}

	if err := st.DeleteConversation(conv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetConversation(conv.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestAppendMessageSequencing(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	for i := 0; i < 3; i++ {
		m, err := st.AppendMessage("conv-x", "user", "msg")
		if err != nil {
			t.Fatal(err)
		}
		if m.Seq != int64(i) {
			t.Fatalf("seq = %d, want %d", m.Seq, i)
		}
	}
	if n, _ := st.MessageCount("conv-x"); n != 3 {
		t.Fatalf("count = %d, want 3", n)
	}
}

func TestDeleteRemovesEverything(t *testing.T) {
	st := openTemp(t)
	defer st.Close()

	if _, err := st.CreateConversation("cloud", "t"); err != nil {
		t.Fatal(err)
	}
	convs, _ := st.ListConversations(true)
	if len(convs) != 1 {
		t.Fatalf("want 1 conversation, got %d", len(convs))
	}
	id := convs[0].ID
	_, _ = st.AppendMessage(id, "user", "hello")
	if err := st.DeleteConversation(id); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetConversation(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestRecordRun(t *testing.T) {
	st := openTemp(t)
	defer st.Close()
	if err := st.RecordRun("conv-x", "cloud", time.Now(), 1234, 0, "", ""); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}
}
