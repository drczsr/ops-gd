package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gongdan/internal/config"
	"gongdan/internal/db"
	"gongdan/internal/gsconfig"
	"gongdan/internal/gsconfig/cos"
	"gongdan/internal/handler"
	"gongdan/internal/mergepreview"
	"gongdan/internal/service/executor"
	"gongdan/internal/service/member"
	"gongdan/internal/service/notify"
	"gongdan/internal/service/order"
	"gongdan/internal/settings"

	"github.com/gin-gonic/gin"
	mysqldrv "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// expandHome 把路径开头的 ~ 展开成当前用户家目录(Go 不会自动展开 ~)。
// 仅用于本机读取的路径(如 SSH 私钥);远程路径不要用它。
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	importPath := flag.String("import-gsconfig", "", "从指定 ServerConfigList.txt 初始化配置库后退出")
	importForce := flag.Bool("force", false, "配置库已有数据时先清空再导入")
	importMergePath := flag.String("import-mergepreview", "", "从指定 MergeServerFunction.txt(GBK)一次性导入/覆盖合服预告库后退出")
	flag.Parse()

	gdb, err := db.Init(cfg.Database.DSN)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}

	launchDelay := time.Duration(cfg.Executor.LaunchDelayMs) * time.Millisecond
	if cfg.Executor.LaunchDelayMs == 0 {
		launchDelay = 400 * time.Millisecond // 默认错峰400ms
	}
	hotBackoffs := make([]time.Duration, 0, len(cfg.Executor.HotRetryBackoffsS))
	for _, s := range cfg.Executor.HotRetryBackoffsS {
		hotBackoffs = append(hotBackoffs, time.Duration(s)*time.Second)
	}
	if len(hotBackoffs) == 0 {
		hotBackoffs = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second}
	}
	gmScript := cfg.Executor.GMHotUpdateScript
	if gmScript == "" {
		gmScript = "/export/packages/scripts/gd_gmhot.sh"
	}
	realCfg := &executor.RealConfig{
		SSHKey:             expandHome(cfg.Executor.SSHKey), // 支持 ~ 展开(本机私钥路径)
		SSHPort:            cfg.Executor.SSHPort,
		SSHUser:            cfg.Executor.SSHUser,
		PackagesDir:        cfg.Executor.PackagesDir,
		RemoteScriptsDir:   cfg.Executor.RemoteScriptsDir,
		MergeScript:        cfg.Executor.MergeScript,
		ServerConfigFile:   cfg.Executor.ServerConfigFile,
		Concurrency:        cfg.Executor.Concurrency,
		SwapConcurrency:    cfg.Executor.SwapConcurrency,
		BatchSize:          cfg.Executor.BatchSize, // 0 时执行器默认50
		LaunchDelay:        launchDelay,
		PerHostConcurrency: cfg.Executor.PerHostConcurrency, // 0 时默认3
		SSHRetries:         cfg.Executor.SSHRetries,         // 0 时默认3
		SSHRetryBackoff:    time.Duration(cfg.Executor.SSHRetryBackoffMs) * time.Millisecond,
		SSHMultiplex:       cfg.Executor.SSHMultiplex,
		StatusPollInterval: time.Duration(cfg.Executor.StatusPollS) * time.Second,
		StatusTimeout:      time.Duration(cfg.Executor.StatusTimeoutS) * time.Second,
		HefuDir:            cfg.Executor.HefuDir,
		StopRetries:        cfg.Executor.StopRetries,
		HotRetryBackoffs:   hotBackoffs,
		GMHotUpdateScript:  gmScript,
	}
	defaults := settings.Settings{
		Mode:           cfg.Executor.Mode,
		AutoExecute:    cfg.Workflow.AutoExecuteAfterApprove,
		AutoOpen:       cfg.Workflow.AutoOpenAfterTest,
		ShowAllServers: cfg.Executor.ShowAllServers,
	}
	settingsStore, err := settings.NewStore(defaults, "settings.json")
	if err != nil {
		log.Printf("加载 settings.json 失败,使用默认值: %v", err)
	}
	var notifier order.Notifier
	if cfg.Notify.Wework.Enabled && cfg.Notify.Wework.Webhook != "" {
		notifier = notify.NewWeworkBot(cfg.Notify.Wework.Webhook)
	}
	orderSvc := order.New(gdb, settingsStore, realCfg).WithAutomation(
		time.Duration(cfg.Workflow.SchedulerTickS) * time.Second,
	).WithNotify(notifier, cfg.Notify.Wework.BaseURL)
	orderSvc.StartScheduler(context.Background())
	memberSvc := member.New(gdb)

	if cfg.GSConfig.DBDSN == "" {
		log.Fatal("未配置 gsconfig.db_dsn:配置库是工单系统的唯一服务器数据来源,必须配置")
	}
	if err := ensureMySQLDatabase(cfg.GSConfig.DBDSN); err != nil {
		log.Fatalf("自动创建配置库失败: %v", err)
	}
	cdb, err := gorm.Open(gormmysql.Open(cfg.GSConfig.DBDSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("打开配置库失败: %v", err)
	}
	if sqlDB, perr := cdb.DB(); perr != nil {
		log.Fatalf("取配置库连接失败: %v", perr)
	} else if perr := sqlDB.Ping(); perr != nil {
		log.Fatalf("配置库连通性校验失败: %v", perr)
	}
	tuneMySQLPool(cdb)
	gsStore := gsconfig.NewStore(cdb)
	if err := gsStore.EnsureRegionColumn(); err != nil {
		log.Printf("[warn] 确保 WoRegion 列失败(不阻塞启动): %v", err)
	}
	gsUploader, err := cos.New(cfg.GSConfig.COS.Region, cfg.GSConfig.COS.Bucket,
		cfg.GSConfig.COS.SecretID, cfg.GSConfig.COS.SecretKey)
	if err != nil {
		log.Fatalf("初始化 COS 失败: %v", err)
	}
	gsSvc := gsconfig.NewService(gsStore, gsUploader,
		cfg.GSConfig.COS.ServerObjectKey, cfg.GSConfig.COS.ClientObjectKey,
		cfg.GSConfig.ServerDebugPath, cfg.GSConfig.ClientDebugPath)
	gsSource := gsconfig.NewServerSource(gsStore)
	realCfg.Source = gsSource
	realCfg.ConfigStore = gsStore
	realCfg.ConfigPublisher = gsSvc
	realCfg.Creator = gsStore
	realCfg.Deleter = gsStore
	realCfg.COS = gsUploader
	realCfg.Artifacts = orderSvc
	realCfg.ALB = &executor.ALBConfig{
		Enabled:               cfg.ALB.Enabled,
		Region:                cfg.ALB.Region,
		ResourceGroupID:       cfg.ALB.ResourceGroupID,
		VpcID:                 cfg.ALB.VpcID,
		HealthCheckTemplateID: cfg.ALB.HealthCheckTemplate,
		LoginLoadBalancerID:   cfg.ALB.LoginLoadBalancerID,
		LoginListenerID:       cfg.ALB.LoginListenerID,
		PayLoadBalancerID:     cfg.ALB.PayLoadBalancerID,
		PayListenerID:         cfg.ALB.PayListenerID,
		WSLoadBalancerID:      cfg.ALB.WSLoadBalancerID,
		WSListenerID:          cfg.ALB.WSListenerID,
	}

	if *importPath != "" {
		if gsSvc == nil {
			log.Fatal("未配置 gsconfig.db_dsn,无法导入")
		}
		raw, err := os.ReadFile(*importPath)
		if err != nil {
			log.Fatalf("读文件失败: %v", err)
		}
		cols, meta, rows, err := gsconfig.Parse(raw)
		if err != nil {
			log.Fatalf("解析失败: %v", err)
		}
		store := gsSvc.Store()
		existing, _ := store.AllRows()
		if len(existing) > 0 && !*importForce {
			log.Fatalf("配置库已有 %d 个服;如需重导请加 -force", len(existing))
		}
		// -force:先删表再重建,这样新文件若有新增列也能建进 schema
		//(EnsureSchema 是 CREATE IF NOT EXISTS、Truncate 只删数据不改结构,均加不上新列)。
		if *importForce {
			if err := store.DropManaged(); err != nil {
				log.Fatalf("删旧表失败: %v", err)
			}
		}
		if err := store.EnsureSchema(cols); err != nil {
			log.Fatalf("建表失败: %v", err)
		}
		if err := store.ImportAll(cols, meta, rows); err != nil {
			log.Fatalf("导入失败: %v", err)
		}
		// 重建 schema(-force 走 DropManaged+EnsureSchema)会按文件列重建,
		// 不含工单系统自维护的 WoRegion 列;导入后补建,避免重导后大区列丢失。
		if err := store.EnsureRegionColumn(); err != nil {
			log.Fatalf("补建 WoRegion 列失败: %v", err)
		}
		log.Printf("导入完成:%d 列,%d 个服", len(cols), len(rows))
		return
	}

	r := gin.Default()
	r.SetFuncMap(handler.TemplateFuncMap())
	r.LoadHTMLGlob("templates/*.html")
	r.Static("/static", "./static")

	var mergePrevSvc *mergepreview.Service
	mpDSN, err := cfg.MergePreviewDSN()
	if err != nil {
		log.Fatalf("合服预告库 DSN 解析失败: %v", err)
	}
	if mpDSN != "" {
		// 首启自动建库:同一 MySQL 实例上新建 database(不存在才建),省去手工 CREATE DATABASE。
		if err := ensureMySQLDatabase(mpDSN); err != nil {
			log.Fatalf("自动创建合服预告库失败: %v", err)
		}
		mpdb, err := gorm.Open(gormmysql.Open(mpDSN), &gorm.Config{})
		if err != nil {
			log.Fatalf("打开合服预告库失败: %v", err)
		}
		tuneMySQLPool(mpdb)
		mpStore := mergepreview.NewStore(mpdb)
		if err := mpStore.EnsureSchema(); err != nil {
			log.Fatalf("合服预告库建表失败: %v", err)
		}
		mergePrevSvc = mergepreview.NewService(mpStore)
	}

	// 命令行一次性导入合服预告全量(GBK 文件→解码→清空重灌),导完即退。
	if *importMergePath != "" {
		if mergePrevSvc == nil {
			log.Fatal("合服预告库未启用(gsconfig.db_dsn 为空),无法导入")
		}
		raw, err := os.ReadFile(*importMergePath)
		if err != nil {
			log.Fatalf("读文件失败: %v", err)
		}
		text, err := mergepreview.DecodeAuto(raw)
		if err != nil {
			log.Fatalf("文件解码失败: %v", err)
		}
		if err := mergePrevSvc.ImportOverwrite(text); err != nil {
			log.Fatalf("导入失败: %v", err)
		}
		n, _ := mergePrevSvc.Count("")
		log.Printf("合服预告导入完成:%s,共 %d 行", *importMergePath, n)
		return
	}

	h := handler.New(cfg, gdb, orderSvc, memberSvc, settingsStore, gsSvc, mergePrevSvc, gsUploader)
	h.Register(r)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Printf("启动于 http://localhost%s (登录 admin / admin123)", addr)
	if err := r.Run(addr); err != nil {
		log.Fatal(err)
	}
}

// tuneMySQLPool 设置连接池存活/空闲上限,避免连接被 MySQL(wait_timeout)
// 单方面关闭后,池中残留的失效连接被复用导致 "invalid connection"。
func tuneMySQLPool(g *gorm.DB) {
	sqlDB, err := g.DB()
	if err != nil {
		return
	}
	sqlDB.SetConnMaxLifetime(3 * time.Minute) // 连接最长存活;短于 MySQL 默认 wait_timeout
	sqlDB.SetConnMaxIdleTime(time.Minute)     // 空闲超过即回收
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(20)
}

// ensureMySQLDatabase 在 DSN 指向的 MySQL 实例上建库(不存在才建),省去手工 CREATE DATABASE。
// 先连到实例(清空 DBName)再 CREATE DATABASE IF NOT EXISTS。非 MySQL DSN/无库名则跳过。
func ensureMySQLDatabase(dsn string) error {
	mc, err := mysqldrv.ParseDSN(dsn)
	if err != nil || mc.DBName == "" {
		return nil // 解析不了或没指定库名,交给后续 Open 报错
	}
	dbName := mc.DBName
	mc.DBName = "" // 先连到实例(不指定库)才能建库
	instDB, err := gorm.Open(gormmysql.Open(mc.FormatDSN()), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("连接 MySQL 实例失败: %w", err)
	}
	defer func() {
		if sqlDB, _ := instDB.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	}()
	if err := instDB.Exec("CREATE DATABASE IF NOT EXISTS `" + dbName + "` DEFAULT CHARACTER SET utf8mb4").Error; err != nil {
		return fmt.Errorf("自动创建库 %s 失败: %w", dbName, err)
	}
	return nil
}
