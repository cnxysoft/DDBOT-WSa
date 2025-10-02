package utils

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestExecWithRunas(t *testing.T) {
	// 仅在 Windows 系统上运行此测试
	if runtime.GOOS != "windows" {
		t.Skip("跳过测试：runas 仅在 Windows 上可用")
	}

	// 测试 runas 命令是否可用
	_, err := exec.LookPath("runas")
	if err != nil {
		t.Skip("跳过测试：系统中未找到 runas 命令")
	}

	// 测试用例1：尝试执行一个简单的命令
	t.Run("执行简单命令", func(t *testing.T) {
		// 使用 runas 执行 whoami 命令
		cmd := exec.Command("runas", "/trustlevel:0x20000", "whoami")
		
		// 设置一个超时时间，避免命令卡住
		timer := time.AfterFunc(5*time.Second, func() {
			cmd.Process.Kill()
		})
		defer timer.Stop()
		
		output, err := cmd.CombinedOutput()
		
		// 处理 Windows 中文编码问题
		if runtime.GOOS == "windows" {
			if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
				output = decoded
			}
		}
		
		fmt.Printf("runas whoami 输出: %s\n", string(output))
		if err != nil {
			fmt.Printf("runas whoami 错误: %v\n", err)
		}
		
		// 检查是否有任何输出
		if len(output) > 0 {
			t.Logf("runas 命令产生了输出: %s", string(output))
		} else {
			t.Log("runas 命令没有产生输出")
		}
	})

	// 测试用例2：尝试执行 notepad
	t.Run("执行记事本", func(t *testing.T) {
		// 查找 notepad 是否存在
		notepadPath, err := exec.LookPath("notepad")
		if err != nil {
			t.Skip("跳过测试：系统中未找到 notepad")
		}

		cmd := exec.Command("runas", "/trustlevel:0x20000", notepadPath)
		
		// 设置一个超时时间，避免命令卡住
		timer := time.AfterFunc(5*time.Second, func() {
			cmd.Process.Kill()
		})
		defer timer.Stop()
		
		output, err := cmd.CombinedOutput()
		
		// 处理 Windows 中文编码问题
		if runtime.GOOS == "windows" {
			if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
				output = decoded
			}
		}
		
		fmt.Printf("runas notepad 输出: %s\n", string(output))
		if err != nil {
			fmt.Printf("runas notepad 错误: %v\n", err)
		}
		
		// 检查是否有任何输出
		if len(output) > 0 {
			t.Logf("runas notepad 命令产生了输出: %s", string(output))
		} else {
			t.Log("runas notepad 命令没有产生输出")
		}
	})

	// 测试用例3：测试不带 /trustlevel 参数的 runas
	t.Run("不带trustlevel参数", func(t *testing.T) {
		// 这个测试可能会触发 UAC 对话框，所以仅在交互式环境下运行
		if os.Getenv("CI") != "" {
			t.Skip("跳过测试：在 CI 环境中不测试需要用户交互的命令")
		}

		cmd := exec.Command("runas", "/user:Administrator", "whoami")
		
		// 设置一个超时时间，避免命令卡住
		timer := time.AfterFunc(5*time.Second, func() {
			cmd.Process.Kill()
		})
		defer timer.Stop()
		
		output, err := cmd.CombinedOutput()
		
		// 处理 Windows 中文编码问题
		if runtime.GOOS == "windows" {
			if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
				output = decoded
			}
		}
		
		fmt.Printf("runas /user:Administrator whoami 输出: %s\n", string(output))
		if err != nil {
			fmt.Printf("runas /user:Administrator whoami 错误: %v\n", err)
		}
		
		// 检查是否有任何输出
		if len(output) > 0 {
			t.Logf("runas /user:Administrator whoami 命令产生了输出: %s", string(output))
		} else {
			t.Log("runas /user:Administrator whoami 命令没有产生输出")
		}
	})
	
	// 测试用例4：测试 runas 执行 cmd 命令
	t.Run("执行cmd命令", func(t *testing.T) {
		cmd := exec.Command("runas", "/trustlevel:0x20000", "cmd /c echo Hello World")
		
		// 设置一个超时时间，避免命令卡住
		timer := time.AfterFunc(5*time.Second, func() {
			cmd.Process.Kill()
		})
		defer timer.Stop()
		
		output, err := cmd.CombinedOutput()
		
		// 处理 Windows 中文编码问题
		if runtime.GOOS == "windows" {
			if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(output); err == nil {
				output = decoded
			}
		}
		
		fmt.Printf("runas cmd /c echo Hello World 输出: %s\n", string(output))
		if err != nil {
			fmt.Printf("runas cmd /c echo Hello World 错误: %v\n", err)
		}
		
		// 检查是否有任何输出
		if len(output) > 0 {
			t.Logf("runas cmd /c echo Hello World 命令产生了输出: %s", string(output))
		} else {
			t.Log("runas cmd /c echo Hello World 命令没有产生输出")
		}
	})
}