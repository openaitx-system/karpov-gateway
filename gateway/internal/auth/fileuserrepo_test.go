package auth

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileUserRepo_CreateLoadCycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")

	r1, err := NewFileUserRepo(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{
		Email:        "admin@example.com",
		PasswordHash: "h",
		Status:       "active",
		Role:         RoleSuperAdmin,
		CreatedAt:    time.Now().UTC(),
	}
	if err := r1.Create(context.Background(), u); err != nil {
		t.Fatalf("create: %v", err)
	}
	if u.ID == "" {
		t.Fatal("expected auto-assigned ID")
	}

	// 模拟进程重启：新建一个 repo 从同一文件 load
	r2, err := NewFileUserRepo(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := r2.GetByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("getByEmail: %v", err)
	}
	if got == nil || got.ID != u.ID || got.Role != RoleSuperAdmin {
		t.Errorf("survived data wrong: %+v", got)
	}
}

func TestFileUserRepo_PersistAfterUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	r, err := NewFileUserRepo(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u := &User{Email: "u@a.com", PasswordHash: "h", Status: "active"}
	if err := r.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	u.Role = RoleAdmin
	if err := r.Update(ctx, u); err != nil {
		t.Fatal(err)
	}
	r2, err := NewFileUserRepo(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := r2.GetByID(ctx, u.ID)
	if got == nil || got.Role != RoleAdmin {
		t.Errorf("update not persisted: %+v", got)
	}
}

func TestFileUserRepo_DuplicateEmailRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	r, _ := NewFileUserRepo(path, nil)
	ctx := context.Background()
	_ = r.Create(ctx, &User{Email: "x@a.com", PasswordHash: "h"})
	err := r.Create(ctx, &User{Email: "x@a.com", PasswordHash: "h2"})
	if !errors.Is(err, ErrUserExists) {
		t.Errorf("dup email must return ErrUserExists, got %v", err)
	}
}

func TestFileUserRepo_EmptyPathStaysInMemory(t *testing.T) {
	r, err := NewFileUserRepo("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path() != "" {
		t.Errorf("Path() = %q, want empty", r.Path())
	}
	if err := r.Create(context.Background(), &User{Email: "a@b.com", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
}

func TestFileUserRepo_FileFormatStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	r, _ := NewFileUserRepo(path, nil)
	_ = r.Create(context.Background(), &User{Email: "a@b.com", PasswordHash: "hh", Role: RoleAdmin})
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Version int `json:"version"`
		NextID  int `json:"next_id"`
		Users   []struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"users"`
	}
	if err := json.Unmarshal(body, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Version != 1 || len(snap.Users) != 1 || snap.Users[0].Email != "a@b.com" || snap.Users[0].Role != RoleAdmin {
		t.Errorf("file schema regression: %s", body)
	}
}

func TestFileUserRepo_ParentDirAutoCreate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deep", "down", "users.json")
	r, err := NewFileUserRepo(path, nil)
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_ = r.Create(context.Background(), &User{Email: "z@z.com", PasswordHash: "h"})
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestFileUserRepo_BootstrapIdempotentAcrossReopens(t *testing.T) {
	// 模拟 bootstrap 的真实路径：第一个 repo 走 bootstrap 创建；
	// 第二次启动时 GetByEmail 必须命中，bootstrap 应跳过。
	path := filepath.Join(t.TempDir(), "users.json")

	open := func() *FileUserRepo {
		r, err := NewFileUserRepo(path, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r1 := open()
	if u, _ := r1.GetByEmail(context.Background(), "admin@example.com"); u != nil {
		t.Fatal("first launch must NOT have superadmin")
	}
	_ = r1.Create(context.Background(), &User{
		Email:        "admin@example.com",
		PasswordHash: "fake-argon2",
		Role:         RoleSuperAdmin,
		Status:       "active",
		CreatedAt:    time.Now().UTC(),
	})

	for i := 0; i < 3; i++ {
		r := open()
		got, err := r.GetByEmail(context.Background(), "admin@example.com")
		if err != nil || got == nil {
			t.Fatalf("restart #%d: superadmin missing: %v", i, err)
		}
		if got.Role != RoleSuperAdmin {
			t.Errorf("restart #%d: role drift: %s", i, got.Role)
		}
	}
}
