package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"v2way/internal/accessgate"
	"v2way/internal/admin"
	"v2way/internal/certificate"
	"v2way/internal/config"
	"v2way/internal/gateway"
	"v2way/internal/store"
	"v2way/web"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }
func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：v2way init | serve | check | import | migrate | recover；各命令可使用 -h 查看参数")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := f.String("data", ".data", "受保护的数据目录")
	username := f.String("username", "admin", "初始化管理员名称")
	listenHost := f.String("listen-host", "0.0.0.0", "站点业务监听的绑定 IP，端口由站点设置决定")
	adminAddr := f.String("admin", "127.0.0.1:4033", "管理监听地址")
	file := f.String("file", "", "check/import 的配置文件，或 migrate 的旧数据导出文件")
	recoverVersion := f.Int64("version", 0, "recover 显式选择的历史版本")
	stdin := f.Bool("password-stdin", false, "init 从标准输入读取一行密码")
	var roots, origins list
	f.Var(&roots, "static-root", "允许托管的静态目录，默认 /www；显式指定时覆盖默认值，可重复指定")
	f.Var(&origins, "origin", "额外允许的管理页面来源，可重复指定")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("存在未知参数")
	}
	if args[0] == "migrate" {
		return migrate(*dir, *file)
	}
	if args[0] == "recover" {
		data, err := filepath.Abs(*dir)
		if err != nil {
			return err
		}
		data, err = filepath.EvalSymlinks(data)
		if err != nil {
			return err
		}
		return store.Recover(data, *recoverVersion, func(c config.Config) error {
			roots, err = resolveStaticRoots(roots, data)
			if err != nil {
				return err
			}

			certs, err := certificate.Open(data)
			if err != nil {
				return err
			}
			defer certs.Automation().Close()
			e := gateway.New(roots, certs)
			defer e.Close()
			snap, err := e.Compile(c)
			if err != nil {
				return err
			}
			snap.Close()
			return nil
		})
	}
	if args[0] == "check" {
		if *file == "" {
			return errors.New("check 需要 -file")
		}
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		c, err := store.DecodeConfig(raw)
		if err != nil {
			return err
		}
		issues := config.Validate(c)
		if len(issues) > 0 {
			b, _ := json.MarshalIndent(issues, "", "  ")
			return errors.New(string(b))
		}
		fmt.Println("配置结构校验通过（未检查运行资源）")
		return nil
	}
	if args[0] != "init" && args[0] != "serve" && args[0] != "import" {
		return errors.New("未知命令")
	}
	data, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	s, err := store.Open(data)
	if err != nil {
		return err
	}
	defer s.Close()
	data, err = filepath.EvalSymlinks(data)
	if err != nil {
		return err
	}
	if args[0] == "import" {
		if *file == "" {
			return errors.New("import 需要 -file")
		}
		raw, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		c, err := store.DecodeConfig(raw)
		if err != nil {
			return err
		}
		d, err := s.Draft()
		if err != nil {
			return err
		}
		if _, err = s.SaveDraft(c, d.Revision); err != nil {
			return err
		}
		fmt.Println("配置已导入草稿，尚未发布。")
		return nil
	}
	if args[0] == "init" {
		var password string
		if *stdin {
			reader := bufio.NewReader(io.LimitReader(os.Stdin, 1026))
			line, err := reader.ReadString('\n')
			if err != nil && len(line) == 0 {
				return errors.New("无法读取密码")
			}
			password = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		} else {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("非交互环境请使用 -password-stdin")
			}
			fmt.Fprint(os.Stderr, "设置管理员密码（至少 8 个字符）：")
			value, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			password = string(value)
			fmt.Fprint(os.Stderr, "再次输入密码：")
			again, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
			if password != string(again) {
				return errors.New("两次密码不一致")
			}
		}
		if err = admin.Initialize(s, *username, password); err != nil {
			return err
		}
		fmt.Println("管理员初始化完成；没有启动任何服务。")
		return nil
	}
	initialPassword, err := admin.InitializeOnStartup(s)
	if err != nil {
		return err
	}
	if initialPassword != "" {
		log.Printf("首次启动已初始化管理员；账号：admin；初始密码：%s；此凭据仅显示一次，请登录后修改密码", initialPassword)
	}
	if err = admin.ValidateAdminAddress(*adminAddr); err != nil {
		return err
	}
	roots, err = resolveStaticRoots(roots, data)
	if err != nil {
		return err
	}

	certs, err := certificate.Open(data)
	if err != nil {
		return err
	}
	gate := accessgate.New(s)
	defer gate.Close()
	engine := gateway.New(roots, certs)
	engine.Access = gate
	defer engine.Close()
	active, err := s.Active()
	if err != nil {
		return fmt.Errorf("读取已发布配置失败：%w", err)
	}
	snap, err := engine.Compile(active.Config)
	if err != nil {
		return fmt.Errorf("加载已发布配置失败：%w", err)
	}
	snap.Version = active.ID
	defer snap.Close()
	listeners := gateway.NewListeners(engine, *listenHost)
	defer listeners.Close()
	handler := admin.New(s, engine, certs, admin.Options{Origins: origins, StaticRoots: roots, Listeners: listeners, AdminAddress: *adminAddr, UI: web.Handler()})
	listener, err := net.Listen("tcp", *adminAddr)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := admin.HTTPServer(*adminAddr, handler)
	server.ErrorLog = log.New(os.Stderr, "http: ", log.LstdFlags)
	plan, err := listeners.Prepare(snap)
	if err != nil {
		snap.Close()
		return err
	}
	plan.Activate(snap)
	failures := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			failures <- err
		}
	}()
	log.Printf("v2way 已启动；管理入口 %s，已启用 %d 个站点业务端口", *adminAddr, len(listeners.Info()))
	certs.Automation().Start()
	defer certs.Automation().Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	handler.StartSessionCleanup(ctx)
	defer stop()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failures:
	case serveErr = <-listeners.Errors():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		server.Close()
	}
	listeners.Close()
	engine.Close()
	return serveErr
}

func resolveStaticRoots(roots []string, data string) ([]string, error) {
	defaults := len(roots) == 0
	if defaults {
		roots = []string{"/www"}
	}
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			// An unused default directory need not exist. The gateway cannot
			// serve it until it exists and a restart acquires its root handle.
			if !defaults || !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		} else {
			absolute = canonical
		}
		for _, pair := range [][2]string{{absolute, data}, {data, absolute}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return nil, errors.New("批准的静态根目录不能包含数据目录，也不能位于数据目录内")
			}
		}
		resolved = append(resolved, absolute)
	}
	return resolved, nil
}
