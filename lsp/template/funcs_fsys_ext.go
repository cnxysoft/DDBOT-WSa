package template

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func openFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		logger.Errorf("template: openFile <%v> error %v", path, err)
		return nil
	}
	return data
}

func updateFile(path string, data string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Errorf("template: openFile <%v> error %v", path, err)
		return err
	}
	defer file.Close()
	_, err = file.WriteString(data)
	if err != nil {
		logger.Errorf("template: updateFile <%v> error %v", path, err)
		return err
	}
	return nil
}

func writeFile(path string, data string) error {
	err := os.WriteFile(path, []byte(data), 0644)
	if err != nil {
		logger.Errorf("template: writeFile <%v> error %v", path, err)
		return err
	}
	return nil
}

func delFile(path string) error {
	err := os.Remove(path)
	if err != nil {
		logger.Errorf("template: delFile <%v> error %v", path, err)
		return err
	}
	return nil
}

func renameFile(path string, newPath string) error {
	err := os.Rename(path, newPath)
	if err != nil {
		logger.Errorf("template: renameFile <%v> error %v", path, err)
		return err
	}
	return nil
}

func readLine(p string, l int64) string {
	file, err := os.OpenFile(p, os.O_RDONLY, 0666)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var ret string
	for i := int64(0); ; i++ {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if i == l-1 {
			ret = line
			break
		}
	}
	return ret
}

func findReadLine(p string, s string) string {
	file, err := os.OpenFile(p, os.O_RDONLY, 0666)
	if err != nil {
		return ""
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.Contains(line, s) {
			return line
		}
	}
	return ""
}

func findWriteLine(p string, s string, n string) error {
	file, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()
	lines := make([]string, 0)
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if strings.Contains(line, s) {
			lines = append(lines, n)
		} else {
			lines = append(lines, line)
		}
	}
	writer := bufio.NewWriter(file)
	_, err = file.Seek(0, 0)
	if err != nil {
		logger.Errorf("template: seek <%v> error %v", p, err)
		return err
	}
	for _, line := range lines {
		_, err = writer.WriteString(line)
		if err != nil {
			logger.Errorf("template: writeFile <%v> error %v", p, err)
			return err
		}
	}
	writer.Flush()
	return nil
}

func writeLine(p string, l int64, s string) error {
	file, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	if l == 0 {
		_, err = writer.WriteString(s)
		if err != nil {
			logger.Errorf("template: writeFile <%v> error %v", p, err)
			return err
		}
	} else {
		lines := make([]string, 0)
		reader := bufio.NewReader(file)
		for i := int64(0); ; i++ {
			line, err := reader.ReadString('\n')
			if err == io.EOF {
				break
			}
			if i == l-1 {
				lines = append(lines, s)
			} else {
				lines = append(lines, line)
			}
		}
		_, err = file.Seek(0, 0)
		if err != nil {
			logger.Errorf("template: seek <%v> error %v", p, err)
			return err
		}
		for _, line := range lines {
			_, err = writer.WriteString(line)
			if err != nil {
				logger.Errorf("template: writeFile <%v> error %v", p, err)
				return err
			}
		}
	}
	writer.Flush()
	return nil
}

func lsDir(dir string, recursive bool) []string {
	var result []string

	files, err := os.ReadDir(dir)
	if err != nil {
		logger.Errorf("lsDir error: %v", err)
		return nil
	}

	for _, item := range files {
		result = append(result, item.Name())

		// 若开启递归且当前项为目录，则递归遍历子目录
		if recursive && item.IsDir() {
			subItems := lsDir(filepath.Join(dir, item.Name()), recursive)
			for _, subItem := range subItems {
				result = append(result, filepath.Join(item.Name(), subItem)) // 添加相对路径
			}
		}
	}

	return result
}
