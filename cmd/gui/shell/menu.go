package shell

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"gpumon/internal/appinfo"
)

// 主選單依規格 6.2 "主選單": 只含各平台慣例上應有的項目, 不重複視窗內已有的功能.
// 文字取自前端的語言資源檔, 語言變更時整份重建.

// role 建立 Wails 的系統標準項目並以 key 的文字取代其英文標籤. macOS 上這些項目以
// 系統的 selector 執行, 行為與一般 Mac 程式相同.
func (s *Shell) role(r application.Role, key string) *application.MenuItem {
	return application.NewRole(r).SetLabel(s.text(key))
}

func (s *Shell) item(key string, onClick func()) *application.MenuItem {
	return application.NewMenuItem(s.text(key)).OnClick(func(*application.Context) { onClick() })
}

func separator() *application.MenuItem { return application.NewMenuItemSeparator() }

// appMenu 為 macOS 的選單列, 依序為 GPU Monitor, Edit, Window, Help.
func (s *Shell) appMenu() *application.Menu {
	app := application.NewMenuFromItems(
		s.item("menu.about", func() { s.OpenAbout(TabAbout) }),
		s.item("menu.settingsMac", s.ShowSettings).SetAccelerator("CmdOrCtrl+,"),
		separator(),
		s.role(application.ServicesMenu, "menu.services"),
		separator(),
		s.role(application.Hide, "menu.hide"),
		s.role(application.HideOthers, "menu.hideOthers"),
		s.role(application.ShowAll, "menu.showAll"),
		separator(),
		s.item("menu.quitMac", s.Quit).SetAccelerator("CmdOrCtrl+q"),
	)
	speech := application.NewMenuFromItems(
		s.role(application.StartSpeaking, "menu.startSpeaking"),
		s.role(application.StopSpeaking, "menu.stopSpeaking"),
	)
	edit := application.NewMenuFromItems(
		s.role(application.Undo, "menu.undo"),
		s.role(application.Redo, "menu.redo"),
		separator(),
		s.role(application.Cut, "menu.cut"),
		s.role(application.Copy, "menu.copy"),
		s.role(application.Paste, "menu.paste"),
		s.role(application.PasteAndMatchStyle, "menu.pasteAndMatchStyle"),
		s.role(application.Delete, "menu.delete"),
		s.role(application.SelectAll, "menu.selectAll"),
		separator(),
		application.NewSubmenu(s.text("menu.speech"), speech),
	)
	window := application.NewMenuFromItems(
		s.role(application.Minimise, "menu.minimize"),
		s.role(application.Zoom, "menu.zoom"),
		separator(),
		s.role(application.BringAllToFront, "menu.bringAllToFront"),
	)
	help := application.NewMenuFromItems(
		s.item("menu.licenses", func() { s.OpenAbout(TabLicenses) }),
	)
	return application.NewMenuFromItems(
		application.NewSubmenu(appinfo.Name, app),
		application.NewSubmenu(s.text("menu.edit"), edit),
		application.NewSubmenu(s.text("menu.window"), window),
		application.NewSubmenu(s.text("menu.help"), help),
	)
}

// windowMenu 為 Windows 主視窗頂端的選單, 依序為 File 與 Help.
func (s *Shell) windowMenu() *application.Menu {
	file := application.NewMenuFromItems(
		s.item("menu.settings", s.ShowSettings),
		separator(),
		s.item("menu.exit", s.Quit),
	)
	help := application.NewMenuFromItems(
		s.item("menu.licenses", func() { s.OpenAbout(TabLicenses) }),
		separator(),
		s.item("menu.about", func() { s.OpenAbout(TabAbout) }),
	)
	return application.NewMenuFromItems(
		application.NewSubmenu(s.text("menu.file"), file),
		application.NewSubmenu(s.text("menu.help"), help),
	)
}

// trayMenu 為匣圖示的選單.
func (s *Shell) trayMenu() *application.Menu {
	return application.NewMenuFromItems(
		s.item("tray.show", s.ShowMain),
		s.item("tray.quit", s.Quit),
	)
}

// menuKeys 為主選單與匣圖示選單所用的語言資源鍵, 供測試確認兩個資源檔皆有定義.
var menuKeys = []string{
	"menu.about", "menu.settingsMac", "menu.services", "menu.hide", "menu.hideOthers",
	"menu.showAll", "menu.quitMac", "menu.edit", "menu.undo", "menu.redo", "menu.cut",
	"menu.copy", "menu.paste", "menu.pasteAndMatchStyle", "menu.delete", "menu.selectAll",
	"menu.speech", "menu.startSpeaking", "menu.stopSpeaking", "menu.window", "menu.minimize",
	"menu.zoom", "menu.bringAllToFront", "menu.help", "menu.licenses", "menu.file",
	"menu.settings", "menu.exit", "tray.show", "tray.quit", "tray.tooltip",
	"tray.tooltipReconnecting",
}
