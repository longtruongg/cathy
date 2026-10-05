package main

import (
	"fmt"
	"log"
	"os"
	_ "time/tzdata"
)

var iconData []byte

func main() {
	exeDir, err := executableDir()
	if err != nil {
		log.Printf("cannot resolve executable directory: %v", err)
	} else if err := os.Chdir(exeDir); err != nil {
		log.Printf("chdir %s: %v", exeDir, err)
	}

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "-help", "--help", "help":
			fmt.Fprintf(os.Stderr, `%s (tray mode)

  katty.exe     Start in system tray (HTTP + mDNS + daily email)
  Put .env next to the exe. Log: katty.log  Data: data\

  Build:
    go build -ldflags="-H windowsgui" -o katty.exe .

  Auto-start: shortcut to katty.exe in shell:startup
`, serviceDisplayName)
			return
		}
	}
	App()
	//if !service.Interactive() {
	//	if err := os.Chdir(exeDir); err != nil {
	//		log.Fatalf("cannot change working directory to %s: %v", exeDir, err)
	//	}
	//} else if _, err := os.Stat(".env"); err != nil {
	//	if _, err := os.Stat(filepath.Join(exeDir, ".env")); err == nil {
	//		if err := os.Chdir(exeDir); err != nil {
	//			log.Fatalf("cannot change working directory to %s: %v", exeDir, err)
	//		}
	//	}
	//}
	//
	//svcConfig := &service.Config{
	//	Name:             serviceName,
	//	DisplayName:      serviceDisplayName,
	//	Description:      serviceDescription,
	//	WorkingDirectory: exeDir,
	//	Option: service.KeyValue{
	//		"StartType":              "automatic",
	//		"OnFailure":              "restart",
	//		"OnFailureDelayDuration": "5s",
	//	},
	//}
	//
	//prg := &Program{}
	//s, err := service.New(prg, svcConfig)
	//if err != nil {
	//	log.Fatalf("cannot create service: %v", err)
	//}
	//
	//if len(os.Args) > 1 {
	//	cmd := os.Args[1]
	//	switch cmd {
	//	case "install", "uninstall", "start", "stop", "restart":
	//		if err := service.Control(s, cmd); err != nil {
	//			log.Fatalf("%s failed: %v", cmd, err)
	//		}
	//		log.Printf("%s %s succeeded", serviceName, cmd)
	//		return
	//	case "status":
	//		st, err := s.Status()
	//		if err != nil {
	//			log.Fatalf("status failed: %v", err)
	//		}
	//		switch st {
	//		case service.StatusRunning:
	//			fmt.Println("running")
	//		case service.StatusStopped:
	//			fmt.Println("stopped")
	//		default:
	//			fmt.Println("unknown")
	//		}
	//		return
	//	case "-h", "-help", "--help", "help":
	//		printUsage()
	//		return
	//	default:
	//		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
	//		printUsage()
	//		os.Exit(2)
	//	}
	//}
	//
	//if err := s.Run(); err != nil {
	//	log.Fatalf("service run failed: %v", err)
	//}
}
