package main

import "sync"

var (
	subject       = "Bin Daily activities"
	excelMutex    sync.Mutex
	categoryCache = map[string]string{}
	cacheMutex    sync.RWMutex
	sendTime      = "40 20 * * *" // 20:40pm
	activeFile    string
	sheetName     = "Events"
	dataDir       string
	listenAddr    = ":8080"
)

const (
	serviceName        = "Kathy"
	serviceDisplayName = "Kathy Activity Tracker"
	serviceDescription = "Receives Android app activity events and emails a daily Excel report"
)

var packageCategories = map[string]string{
	"com.twitter.android":             "Social",
	"com.facebook.katana":             "Facebook",
	"com.instagram.android":           "Instagram",
	"com.zhiliaoapp.musically":        "Tik Tok",
	"com.instagram.barcelona":         "Thread",
	"com.ss.android.ugc.aweme":        "Douyin",
	"com.ss.android.ugc.aweme.mobile": "Douyin",

	"com.supercell.clashofclans":              "Class of Clan",
	"com.mojang.minecraftpe":                  "Minecraft",
	"com.dts.freefireth":                      "Free Fire Game",
	"com.dts.freefiremax":                     "Free Fire Game",
	"com.riotgames.league.wildriftvn":         "LMHT: Toc Chien",
	"com.garena.game.kgvn":                    "Garena Lien Quan Mobile",
	"com.roblox.client":                       "Roblox Game",
	"com.roblox.client.vnggames":              "Roblox Viet Nam",
	"com.riotgames.league.teamfighttacticsvn": "Đấu Trường Chân Lý",
	"com.riotgames.league.teamfighttactics":   "Đấu Trường Chân Lý",

	"com.google.android.youtube": "Youtube",
	"com.netflix.mediaclient":    "Netflix",

	"com.microsoft.emmx":  "Browser",
	"com.android.chrome":  "Chrome",
	"org.mozilla.firefox": "Firefox",

	"com.shopee.vn":           "Shopee",
	"vn.tiki.app.tikiandroid": "Tik Tok shop",
}

func getCategory(packageName string) string {
	cacheMutex.RLock()
	if cat, ok := categoryCache[packageName]; ok {
		cacheMutex.RUnlock()
		return cat
	}
	cacheMutex.RUnlock()

	cat, ok := packageCategories[packageName]
	if !ok {
		cat = packageName // search by hands
	}
	cacheMutex.Lock()
	categoryCache[packageName] = cat
	cacheMutex.Unlock()
	return cat
}
