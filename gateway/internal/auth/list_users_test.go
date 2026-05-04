package auth

import (
	"context"
	"testing"
	"time"
)

func seedUsers(t *testing.T, repo *MemUserRepo) {
	t.Helper()
	now := time.Now().UTC()
	users := []User{
		{Email: "alice@example.com", Status: "active", Role: RoleUser, CreatedAt: now.Add(-3 * time.Hour)},
		{Email: "bob@example.com", Status: "active", Role: RoleAdmin, CreatedAt: now.Add(-2 * time.Hour)},
		{Email: "carol@example.com", Status: "locked", Role: RoleUser, CreatedAt: now.Add(-1 * time.Hour)},
		{Email: "dave@another.org", Status: "active", Role: RoleSuperAdmin, CreatedAt: now},
	}
	for i := range users {
		if err := repo.Create(context.Background(), &users[i]); err != nil {
			t.Fatalf("seed[%d]: %v", i, err)
		}
	}
}

func TestMemUserRepo_ListUsers_All_DescByCreatedAt(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	users, total, err := repo.ListUsers(context.Background(), ListUserFilter{})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected total 4, got %d", total)
	}
	if len(users) != 4 {
		t.Fatalf("expected 4 returned, got %d", len(users))
	}
	if users[0].Email != "dave@another.org" {
		t.Fatalf("expected newest first (dave), got %s", users[0].Email)
	}
	if users[3].Email != "alice@example.com" {
		t.Fatalf("expected oldest last (alice), got %s", users[3].Email)
	}
}

func TestMemUserRepo_ListUsers_FilterByEmail(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	users, total, err := repo.ListUsers(context.Background(), ListUserFilter{EmailLike: "EXAMPLE.COM"})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if total != 3 {
		t.Fatalf("expected 3 examples, got %d", total)
	}
	for _, u := range users {
		if !containsFold(u.Email, "example.com") {
			t.Fatalf("unexpected email: %s", u.Email)
		}
	}
}

func TestMemUserRepo_ListUsers_FilterByRole(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	users, total, _ := repo.ListUsers(context.Background(), ListUserFilter{Role: RoleAdmin})
	if total != 1 || len(users) != 1 || users[0].Email != "bob@example.com" {
		t.Fatalf("expected only bob (admin), got %v", users)
	}
}

func TestMemUserRepo_ListUsers_FilterByStatus(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	users, total, _ := repo.ListUsers(context.Background(), ListUserFilter{Status: "locked"})
	if total != 1 || users[0].Email != "carol@example.com" {
		t.Fatalf("expected only carol (locked), got %v", users)
	}
}

func TestMemUserRepo_ListUsers_Pagination(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	page1, total, _ := repo.ListUsers(context.Background(), ListUserFilter{Limit: 2, Offset: 0})
	if total != 4 || len(page1) != 2 {
		t.Fatalf("page1: expected total=4 len=2, got total=%d len=%d", total, len(page1))
	}
	page2, _, _ := repo.ListUsers(context.Background(), ListUserFilter{Limit: 2, Offset: 2})
	if len(page2) != 2 {
		t.Fatalf("page2: expected len=2, got %d", len(page2))
	}
	// 不重叠
	for _, u1 := range page1 {
		for _, u2 := range page2 {
			if u1.ID == u2.ID {
				t.Fatalf("page1 and page2 overlap on %s", u1.ID)
			}
		}
	}
}

func TestService_SetStatus(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	svc := NewService(repo, nil, Options{MinPasswordStrength: -1})
	users, _, _ := repo.ListUsers(context.Background(), ListUserFilter{EmailLike: "alice"})
	if len(users) != 1 {
		t.Fatalf("expected 1 alice")
	}
	if err := svc.SetStatus(context.Background(), users[0].ID, "locked"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), users[0].ID)
	if got.Status != "locked" {
		t.Fatalf("expected locked, got %s", got.Status)
	}

	if err := svc.SetStatus(context.Background(), users[0].ID, "bogus"); err == nil {
		t.Fatalf("expected reject of invalid status")
	}
}

func TestService_AdminResetPassword(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	svc := NewService(repo, nil, Options{MinPasswordStrength: -1})
	user, _ := repo.GetByEmail(context.Background(), "alice@example.com")
	if err := svc.AdminResetPassword(context.Background(), user.ID, "newpass123"); err != nil {
		t.Fatalf("AdminResetPassword: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), user.ID)
	if got.PasswordHash == user.PasswordHash {
		t.Fatalf("password hash should change after reset")
	}
	if got.PasswordHash == "" {
		t.Fatalf("password hash should be set")
	}
}

func TestService_ListUsers_DispatchesToLister(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	svc := NewService(repo, nil, Options{MinPasswordStrength: -1})
	users, total, err := svc.ListUsers(context.Background(), ListUserFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected 4, got %d", total)
	}
	if len(users) != 4 {
		t.Fatalf("expected 4, got %d", len(users))
	}
}

func TestService_SetUserPlan(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	svc := NewService(repo, nil, Options{MinPasswordStrength: -1, APIKeyRepo: NewMemAPIKeyRepo()})
	user, _ := repo.GetByEmail(context.Background(), "alice@example.com")

	// 默认 PlanID 为 "free"（PG default 在 mem repo 上由 EffectivePlanID 兜底）
	if got := user.EffectivePlanID(); got != "free" {
		t.Fatalf("expected free, got %s", got)
	}

	if err := svc.SetUserPlan(context.Background(), user.ID, "enterprise"); err != nil {
		t.Fatalf("SetUserPlan: %v", err)
	}
	u2, _ := repo.GetByID(context.Background(), user.ID)
	if u2.PlanID != "enterprise" {
		t.Fatalf("expected enterprise, got %s", u2.PlanID)
	}

	// SetUserPlan 拒绝空 planID
	if err := svc.SetUserPlan(context.Background(), user.ID, ""); err == nil {
		t.Fatalf("expected reject empty planID")
	}
}

func TestService_CreateAPIKey_InheritsUserPlan(t *testing.T) {
	repo := NewMemUserRepo()
	seedUsers(t, repo)
	keyRepo := NewMemAPIKeyRepo()
	svc := NewService(repo, nil, Options{
		MinPasswordStrength: -1, APIKeyRepo: keyRepo,
		PasswordParams: DefaultPasswordParams(),
	})
	user, _ := repo.GetByEmail(context.Background(), "alice@example.com")

	// 1) 用户初始为 free → 新 Key 继承 free
	_, rec, err := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{Name: "k1"})
	if err != nil {
		t.Fatalf("CreateAPIKey k1: %v", err)
	}
	if rec.PlanID != "free" {
		t.Fatalf("expected free, got %s", rec.PlanID)
	}

	// 2) 把用户切到 enterprise
	if err := svc.SetUserPlan(context.Background(), user.ID, "enterprise"); err != nil {
		t.Fatalf("SetUserPlan: %v", err)
	}

	// 3) 再创建 Key → 应继承 enterprise
	_, rec2, err := svc.CreateAPIKey(context.Background(), user.ID, CreateAPIKeyInput{Name: "k2"})
	if err != nil {
		t.Fatalf("CreateAPIKey k2: %v", err)
	}
	if rec2.PlanID != "enterprise" {
		t.Fatalf("expected enterprise after upgrade, got %s", rec2.PlanID)
	}
}
