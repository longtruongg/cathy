package main

import (
	"log"
	"os/exec"
	"path/filepath"

	"github.com/getlantern/systray"
	"github.com/getlantern/systray/example/icon"
)

var app *Program

func App() {
	systray.Run(onReady, onExist)
}

func onExist() {
	if app != nil {
		app.Shutdown()
	}
}

func onReady() {
	systray.SetTemplateIcon(icon.Data, icon.Data)
	systray.SetTitle("Katty")
	systray.SetTooltip(serviceDisplayName)
	if len(iconData) > 0 {
		systray.SetIcon(iconData)
	}
	mStatus := systray.AddMenuItem(serviceDisplayName, serviceDescription)
	mStatus.Disable()
	systray.AddSeparator()
	mData := systray.AddMenuItem("Open data ", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Exit", "...")
	app := &Program{}
	go func() {
		if err := app.StartAll(); err != nil {
			log.Print("starting failed %v", err)
			mStatus.SetTooltip("Error- sett katty.log")
		}
		mStatus.SetTitle("Katty running on" + listenAddr)
	}()
	go func() {
		for {
			select {
			case <-mData.ClickedCh:
				dir := dataDir
				if dir == "" {
					dir = "data"
				}
				abs, err := filepath.Abs(dir)
				if err != nil {
					abs = dir
				}
				_ = exec.Command("explorer", abs).Start()
			case <-mQuit.ClickedCh:
				systray.Quit()
				return
			}

		}
	}()
}
