// 命令 syso 由 .ico 產生 Windows 執行檔的資源檔 (.syso), 只寫入圖示資源, 不寫入
// application manifest. 圖示的資源 ID 為 3, Wails 以此作為 Windows 視窗圖示.
//
// 用法: syso -ico gpumon.ico -out rsrc_windows_amd64.syso
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	icoPath := flag.String("ico", "", "input .ico file")
	outPath := flag.String("out", "", "output .syso file")
	flag.Parse()
	if *icoPath == "" || *outPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*icoPath, *outPath); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(icoPath, outPath string) error {
	in, err := os.Open(icoPath)
	if err != nil {
		return err
	}
	defer in.Close()
	icon, err := winres.LoadICO(in)
	if err != nil {
		return err
	}
	rs := &winres.ResourceSet{}
	if err := rs.SetIcon(winres.ID(3), icon); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := rs.WriteObject(out, winres.ArchAMD64); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
