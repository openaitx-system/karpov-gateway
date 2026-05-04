/**
 * RBAC 角色定义与判断 helper。
 *
 * 与 gateway/internal/auth/service.go 中 RoleUser/RoleAdmin/RoleSuperAdmin 一一对应。
 * 服务端 / 客户端共用，但敏感 gating 必须在服务端再校验一次（前端 RBAC 仅用于 UI 展示）。
 */
export const Roles = {
  User: "user",
  Admin: "admin",
  SuperAdmin: "superadmin",
} as const;

export type Role = (typeof Roles)[keyof typeof Roles];

const RANK: Record<Role, number> = {
  user: 0,
  admin: 1,
  superadmin: 2,
};

export function isValidRole(v: string | undefined | null): v is Role {
  return v === "user" || v === "admin" || v === "superadmin";
}

export function isAdmin(role: string | undefined | null): boolean {
  return role === Roles.Admin || role === Roles.SuperAdmin;
}

export function isSuperAdmin(role: string | undefined | null): boolean {
  return role === Roles.SuperAdmin;
}

/** atLeast(role, "admin") 判断当前角色权限 ≥ 目标角色 */
export function atLeast(role: string | undefined | null, target: Role): boolean {
  if (!isValidRole(role)) return false;
  return RANK[role] >= RANK[target];
}
