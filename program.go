package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hashicorp/mdns"
	"github.com/kardianos/service"
	"github.com/robfig/cron/v3"
	"gopkg.in/mail.v2"
)

type Program struct {
	cfg        *Config
	httpServer *http.Server
	cron       *cron.Cron
	logFile    *os.File
	mdns       *mdns.Server
}

// StartAll without service { log, exel, cron, http, mDNS, systray}
func (p *Program) StartAll() error {
	if err := p.setup(); err != nil {
		return fmt.Errorf("program starting %w", err)
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if p.cfg != nil {
			p.cron.Stop()
		}
		return fmt.Errorf("program listening  %w", err)
	}
	if srv, err := advertise(ln.Addr().(*net.TCPAddr).Port); err != nil {
		log.Printf("mdns advertise failded %v", err)
	} else {
		p.mdns = srv
	}
	go func() {
		log.Printf("server listening on http  %v", listenAddr)
		if err := p.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http server faild %v", err)
		}
	}()
	return nil
}

// Shutdown without service
func (p *Program) Shutdown() {
	log.Print("program shutting down")
	if p.mdns != nil {
		if err := p.mdns.Shutdown(); err != nil {
			log.Printf("mdns shutdown failed %v", err)
		}
	}
	if p.cron != nil {
		cronContext := p.cron.Stop()
		select {
		case <-cronContext.Done():
		case <-time.After(8 * time.Second):
			log.Print("program cronjob timed out")
		}
	}
	if p.httpServer != nil {
		ctx, cancle := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancle()
		if err := p.httpServer.Shutdown(ctx); err != nil {
			log.Printf("http shutdowns :%v", err)
		}
	}
	log.Print("program shutting down")
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
}
func (p *Program) Start(s service.Service) error {
	if err := p.setup(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if p.cron != nil {
			p.cron.Stop()
		}
		return fmt.Errorf("listen %s: %w", listenAddr, err)
	}
	if srv, err := advertise(ln.Addr().(*net.TCPAddr).Port); err != nil {
		log.Printf("mdns advertise failed: %v", err)
	} else {
		p.mdns = srv
	}
	go func() {
		log.Printf("Server listening on http://%s", listenAddr)
		if err := p.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http server error: %v", err)
		}
	}()
	return nil
}

func (p *Program) setup() error {
	logFile, err := setupFileLog()
	if err != nil {
		log.Printf("cannot open cathy.log: %v (continuing with stdout only)", err)
	} else {
		p.logFile = logFile
	}

	dir, err := ensureDataDir()
	if err != nil {
		return err
	}
	dataDir = dir

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	p.cfg = cfg

	if err := todayFileExists(); err != nil {
		log.Printf("todayFileExists err: %v", err)
	}

	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		return fmt.Errorf("load timezone Asia/Ho_Chi_Minh: %w", err)
	}

	cronb := cron.New(cron.WithLocation(loc))
	if _, err := cronb.AddFunc(sendTime, func() {
		now := time.Now().In(loc)
		log.Printf("Cron triggered at %s — sending daily report", now.Format("2006-01-02 15:04:05 MST"))

		if err := sendJobDaily(p.cfg); err != nil {
			log.Printf("sendJobDaily failed: %v", err)
		} else {
			log.Println("Email sent successfully")
		}
	}); err != nil {
		return fmt.Errorf("failed to schedule cron job: %w", err)
	}
	cronb.Start()
	p.cron = cronb
	mux := MuxHandler()
	p.httpServer = &http.Server{Addr: listenAddr, Handler: mux}
	return nil
}

func (p *Program) Stop(s service.Service) error {
	log.Println("service stopping")
	if p.mdns != nil {
		if err := p.mdns.Shutdown(); err != nil {
			log.Printf("mdns shutdown: %v", err)
		}
	}
	if p.cron != nil {
		cronCtx := p.cron.Stop()
		select {
		case <-cronCtx.Done():
		case <-time.After(8 * time.Second):
			log.Println("timed out waiting for cron jobs")
		}
	}
	if p.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.httpServer.Shutdown(ctx); err != nil {
			log.Printf("http shutdown: %v", err)
		}
	}
	log.Println("service stopped")
	if p.logFile != nil {
		_ = p.logFile.Close()
	}
	return nil
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return filepath.Dir(exe), nil
	}
	return filepath.Dir(resolved), nil
}

func setupFileLog() (*os.File, error) {
	f, err := os.OpenFile("cathy.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	return f, nil
}

func ensureDataDir() (string, error) {
	dir := "data"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cannot create data directory: %w", err)
	}
	return dir, nil
}

func formatDuration(d string) string {
	loc := time.FixedZone("GMT+7", 7*60*60)
	t, err := time.Parse(time.RFC3339Nano, d)
	if err != nil {
		t = time.Now()
	}
	return t.In(loc).Format("2-1-2006 15:04:05 GMT+7")
}
func sendJobDaily(cfg *Config) error {
	if err := todayFileExists(); err != nil {
		return fmt.Errorf("no file exists %s", err)
	}
	excelMutex.Lock()
	fileToSend := activeFile
	excelMutex.Unlock()

	if !fileExists(fileToSend) {
		log.Printf("No data file to send today (%s)", fileToSend)
		return fmt.Errorf("no file to send")
	}

	msg := mail.NewMessage()
	msg.SetHeader("From", cfg.FromMail)
	msg.SetHeader("To", cfg.ToMail)
	msg.SetHeader("Subject", fmt.Sprintf("%s - %s", subject, time.Now().Format("2006-01-02")))
	msg.SetBody("text/plain", "Daily activities attached.")
	msg.Attach(fileToSend)

	dialer := mail.NewDialer("smtp.gmail.com", 587, cfg.FromMail, cfg.AppPassword)
	if err := dialer.DialAndSend(msg); err != nil {
		return fmt.Errorf("cannot send email: %w", err)
	}

	log.Printf("Sent daily report: %s", fileToSend)
	return nil
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `%s

Usage:
  katty.exe              Run in the foreground (or as a Windows service when started by SCM)
  katty.exe install      Install as a Windows service (run as Administrator)
  katty.exe uninstall    Remove the Windows service
  katty.exe start        Start the service
  katty.exe stop         Stop the service
  katty.exe restart      Restart the service
  katty.exe status       Show service status

Put .env next to the executable. Logs are written to cathy.log in that same folder.
`, serviceDisplayName)
}
