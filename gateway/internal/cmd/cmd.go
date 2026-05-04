// Package cmd 是各 service 的 wiring runner 集合。
//
// 设计目标：
//
//	cmd/<name>/main.go      → 独立二进制（向后兼容；适合 k8s 单 Pod 单镜像）
//	cmd/qqmusic-gateway/    → 统一二进制 + subcommand dispatcher（适合
//	                          monolith 部署 / docker compose 单容器跑多服务）
//	internal/cmd/<name>/    → 真实 wiring 函数 Run([]string) error
//
// 两条路径共用 Run，避免 wiring 双份维护。Run 必须满足：
//   - 用 flag.NewFlagSet（不污染全局 flag.CommandLine）
//   - 阻塞到 ctx 取消（SIGINT/SIGTERM）
//   - 返回错误（caller 决定 stderr+exit code）
package cmd

import (
	"context"
	"errors"
	"flag"
)

// Runner 是各 service 的统一签名。
//
// ctx：由 main 统一持有 signal.NotifyContext；ctx.Done() 即所有 service 一齐退出。
// args：已剥掉 program-name 的剩余 flag。实现内部用 flag.NewFlagSet 解析。
type Runner func(ctx context.Context, args []string) error

// ParseFlags 解析 flag set；遇到 -h/-help 时不当错误返回（flag 包已经打了
// usage，调用方应该直接 return nil 当成正常退出）。
//
// 调用方约定：
//
//	stop, err := cmd.ParseFlags(fs, args)
//	if err != nil { return err }
//	if stop { return nil }
func ParseFlags(fs *flag.FlagSet, args []string) (stop bool, err error) {
	err = fs.Parse(args)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, flag.ErrHelp) {
		return true, nil
	}
	return false, err
}
