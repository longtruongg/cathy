package main

type AppOpenedLong struct {
	Timestamp    string `json:"timestamp"`
	AppName      string `json:"appName"`
	Package      string `json:"package"`
	Duration     int64  `json:"duration"`
	SessionStart int64  `json:"sessionStart"`
	DeviceID     string `json:"deviceId"`
}

type PackageChange struct {
	Timestamp string `json:"timestamp"`
	AppName   string `json:"appName"`
	Package   string `json:"package"`
	DeviceID  string `json:"deviceId"`
}

type UninstallEvent struct {
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}
type GameSession struct {
	AppName      string `json:"appName"`
	Package      string `json:"package"`
	SessionStart int64  `json:"sessionStart"`
	SessionEnd   int64  `json:"sessionEnd"`
	Duration     int64  `json:"duration"`
	DeviceID     string `json:"deviceId"`
}
