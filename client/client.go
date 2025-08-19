package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/eiannone/keyboard"
)

// Значение по умолчанию — можно менять в настройках из интерфейса
const defaultServerURL = "http://syncerch.meysner.ru"
const configFile = "config.json"

const asciiLogo = `
                                 
                             _   
 ___ _ _ ___ ___ ___ ___ ___| |_ 
|_ -| | |   |  _| -_|  _|  _|   |
|___|_  |_|_|___|___|_| |___|_|_|
    |___|                        
`

// Цвета ANSI
const (
	reset         = "\x1b[0m"
	bold          = "\x1b[1m"
	dim           = "\x1b[2m"
	invert        = "\x1b[7m"
	black         = "\x1b[30m"
	red           = "\x1b[31m"
	green         = "\x1b[32m"
	yellow        = "\x1b[33m"
	blue          = "\x1b[34m"
	magenta       = "\x1b[35m"
	cyan          = "\x1b[36m"
	white         = "\x1b[37m"
	brightBlack   = "\x1b[90m"
	brightRed     = "\x1b[91m"
	brightGreen   = "\x1b[92m"
	brightYellow  = "\x1b[93m"
	brightBlue    = "\x1b[94m"
	brightMagenta = "\x1b[95m"
	brightCyan    = "\x1b[96m"
	brightWhite   = "\x1b[97m"

	bgBlue    = "\x1b[44m"
	bgMagenta = "\x1b[45m"
)

type Config struct {
	Token      string `json:"token"`
	FolderPath string `json:"folder_path"`
	ServerURL  string `json:"server_url"`
}

func main() {
	cfg := loadConfig()
	reader := bufio.NewReader(os.Stdin)

	// Первичная инициализация
	if strings.TrimSpace(cfg.Token) == "" {
		fmt.Print("Введите токен: ")
		token, _ := reader.ReadString('\n')
		cfg.Token = strings.TrimSpace(token)
	}
	if strings.TrimSpace(cfg.FolderPath) == "" {
		fmt.Print("Введите путь к папке: ")
		fp, _ := reader.ReadString('\n')
		cfg.FolderPath = strings.TrimSpace(fp)
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		cfg.ServerURL = defaultServerURL
	}
	saveConfig(cfg)

	// Открываем чтение клавиатуры
	if err := keyboard.Open(); err != nil {
		fmt.Println("Не удалось открыть клавиатуру:", err)
		return
	}
	defer keyboard.Close()

	selected := 0 // 0=download, 1=upload, 2=settings
	status := ""
	statusColor := "" // brightCyan/info, brightGreen/success, brightRed/error, brightYellow/progress

	for {
		drawUI(selected, cfg, status, statusColor)

		r, key, err := keyboard.GetKey()
		if err != nil {
			status = "Ошибка чтения клавиши: " + err.Error()
			statusColor = brightRed
			continue
		}

		switch key {
		case keyboard.KeyEsc:
			clearScreen()
			return
		case keyboard.KeyArrowLeft:
			if selected == 0 {
				selected = 2
			} else {
				selected--
			}
		case keyboard.KeyArrowRight:
			selected = (selected + 1) % 3
		case keyboard.KeyEnter:
			switch selected {
			case 0: // download
				status = "Скачивание..."
				statusColor = brightYellow
				drawUI(selected, cfg, status, statusColor)
				if err := downloadFolder(cfg.ServerURL, cfg.Token, cfg.FolderPath); err != nil {
					status = "Ошибка скачивания: " + err.Error()
					statusColor = brightRed
				} else {
					status = "Папка успешно скачана"
					statusColor = brightGreen
				}
			case 1: // upload
				status = "Загрузка..."
				statusColor = brightYellow
				drawUI(selected, cfg, status, statusColor)
				if err := uploadFolder(cfg.ServerURL, cfg.Token, cfg.FolderPath); err != nil {
					status = "Ошибка загрузки: " + err.Error()
					statusColor = brightRed
				} else {
					status = "Папка успешно загружена"
					statusColor = brightGreen
				}
			case 2: // settings
				if err := settingsScreen(&cfg, reader); err != nil {
					status = "Ошибка настроек: " + err.Error()
					statusColor = brightRed
				} else {
					status = "Настройки сохранены"
					statusColor = brightGreen
				}
			}
		default:
			if r == 'q' || r == 'Q' {
				clearScreen()
				return
			}
		}
	}
}

func drawUI(selected int, cfg Config, status, sColor string) {
	clearScreen()
	// Логотип
	fmt.Println(brightCyan + asciiLogo + reset)

	// Инфо
	fmt.Printf("%sСервер:%s %s\n", brightBlue, reset, cfg.ServerURL)
	fmt.Printf("%sПапка:%s  %s\n\n", brightBlue, reset, cfg.FolderPath)
	// fmt.Printf("%sТокен:%s  %s\n", brightBlue, reset, maskToken(cfg.Token))

	// Кнопки меню: download, upload, settings
	labels := []string{"download", "upload", "settings"}
	for i, label := range labels {
		if i > 0 {
			fmt.Print("   ")
		}
		if i == selected {
			fmt.Print(buttonSelected(" " + label + " "))
		} else {
			fmt.Print(buttonIdle(" " + label + " "))
		}
	}
	fmt.Println()
	fmt.Println()
	fmt.Println(dim + "← → для выбора, Enter — выполнить, q/Esc — выход" + reset)

	// Статус
	if status != "" {
		fmt.Println()
		if sColor == "" {
			fmt.Println(status)
		} else {
			fmt.Println(sColor + status + reset)
		}
	}
}

func buttonSelected(s string) string {
	return bgBlue + brightWhite + bold + s + reset
}

func buttonIdle(s string) string {
	return brightBlack + s + reset
}

func highlightLineSelected(s string) string {
	return bgMagenta + brightWhite + bold + s + reset
}

func clearScreen() {
	fmt.Print("\x1b[2J\x1b[H")
}

func maskToken(t string) string {
	t = strings.TrimSpace(t)
	if len(t) <= 4 {
		return t
	}
	return t[:2] + strings.Repeat("*", len(t)-4) + t[len(t)-2:]
}

func loadConfig() Config {
	var cfg Config
	file, err := os.Open(configFile)
	if err != nil {
		// файл может отсутствовать — вернём дефолты
		cfg.ServerURL = defaultServerURL
		return cfg
	}
	defer file.Close()
	_ = json.NewDecoder(file).Decode(&cfg)
	if strings.TrimSpace(cfg.ServerURL) == "" {
		cfg.ServerURL = defaultServerURL
	}
	return cfg
}

func saveConfig(cfg Config) {
	file, err := os.Create(configFile)
	if err != nil {
		fmt.Println("Не удалось сохранить конфигурацию:", err)
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(cfg)
}

// =================== NETWORK / IO ===================

func uploadFolder(serverURL, token, folderPath string) error {
	tmpZip := "temp_upload.zip"
	if err := zipFolder(folderPath, tmpZip); err != nil {
		return err
	}
	defer os.Remove(tmpZip)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("folder", filepath.Base(tmpZip))
	if err != nil {
		return err
	}
	file, err := os.Open(tmpZip)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	_ = writer.Close()

	req, err := http.NewRequest("POST", strings.TrimRight(serverURL, "/")+"/upload", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(data))
	}
	return nil
}

func downloadFolder(serverURL, token, folderPath string) error {
	req, err := http.NewRequest("GET", strings.TrimRight(serverURL, "/")+"/download", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", string(data))
	}

	tmpZip := "temp_download.zip"
	out, err := os.Create(tmpZip)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		return err
	}
	out.Close()
	defer os.Remove(tmpZip)

	_ = os.RemoveAll(folderPath)
	if err := os.MkdirAll(folderPath, 0755); err != nil {
		return err
	}
	return unzip(tmpZip, folderPath)
}

func zipFolder(src, dest string) error {
	zipFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	src = filepath.Clean(src)

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// В ZIP всегда пишем с прямыми слэшами — это кроссплатформенно
		name := filepath.ToSlash(rel)

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name

		if info.IsDir() {
			// Явно помечаем директорию
			if !strings.HasSuffix(header.Name, "/") {
				header.Name += "/"
			}
			_, err = archive.CreateHeader(header)
			return err
		}

		header.Method = zip.Deflate
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(writer, f)
		return err
	})
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	dest = filepath.Clean(dest)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}

	for _, f := range r.File {
		// Нормализуем путь из архива:
		// 1) заменяем возможные обратные слэши из Windows на прямые,
		// 2) убираем лидирующие слэши,
		// 3) конвертируем в нативные разделители ОС и чистим путь.
		name := f.Name
		name = strings.ReplaceAll(name, "\\", "/")
		name = strings.TrimLeft(name, "/")
		name = filepath.Clean(filepath.FromSlash(name))

		fpath := filepath.Join(dest, name)

		// Защита от Zip Slip
		if !strings.HasPrefix(fpath, dest+string(os.PathSeparator)) && fpath != dest {
			return fmt.Errorf("illegal file path in zip: %s", f.Name)
		}

		// Директория (иногда архивы не ставят атрибут dir, а ставят суффикс "/")
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return err
			}
			continue
		}

		// Файл
		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}
		if _, err = io.Copy(outFile, rc); err != nil {
			rc.Close()
			outFile.Close()
			return err
		}
		rc.Close()
		outFile.Close()
	}
	return nil
}

// =================== SETTINGS SCREEN ===================

func settingsScreen(cfg *Config, reader *bufio.Reader) error {
	selected := 0
	status := ""
	for {
		drawSettingsUI(selected, *cfg, status)

		r, key, err := keyboard.GetKey()
		if err != nil {
			return err
		}
		switch key {
		case keyboard.KeyEsc:
			return nil
		case keyboard.KeyArrowUp:
			if selected == 0 {
				selected = 3
			} else {
				selected--
			}
		case keyboard.KeyArrowDown:
			selected = (selected + 1) % 4
		case keyboard.KeyEnter:
			switch selected {
			case 0: // folder path
				if val, ok, err := promptLine("\nНовый путь к папке (пусто — отмена): ", reader); err != nil {
					status = "Ошибка ввода: " + err.Error()
				} else if ok {
					cfg.FolderPath = expandPath(val)
					saveConfig(*cfg)
					status = green + "Путь к папке обновлен" + reset
				} else {
					status = dim + "Отменено" + reset
				}
			case 1: // token
				if val, ok, err := promptLine("\nНовый токен (пусто — отмена): ", reader); err != nil {
					status = "Ошибка ввода: " + err.Error()
				} else if ok {
					cfg.Token = strings.TrimSpace(val)
					saveConfig(*cfg)
					status = green + "Токен обновлен" + reset
				} else {
					status = dim + "Отменено" + reset
				}
			case 2: // server URL
				if val, ok, err := promptLine("\nНовый адрес сервера (например http://host:port) (пусто — отмена): ", reader); err != nil {
					status = "Ошибка ввода: " + err.Error()
				} else if ok {
					norm := normalizeURL(val)
					if norm == "" {
						status = red + "Некорректный адрес" + reset
					} else {
						cfg.ServerURL = norm
						saveConfig(*cfg)
						status = green + "Адрес сервера обновлен" + reset
					}
				} else {
					status = dim + "Отменено" + reset
				}
			case 3: // back
				return nil
			}
		default:
			if r == 'q' || r == 'Q' {
				return nil
			}
		}
	}
}

func drawSettingsUI(selected int, cfg Config, status string) {
	clearScreen()
	fmt.Println(brightCyan + asciiLogo + reset)
	fmt.Println(bold + "Настройки" + reset)
	fmt.Println()

	items := []string{
		fmt.Sprintf("Изменить путь к папке   [%s]", cfg.FolderPath),
		fmt.Sprintf("Изменить токен          [%s]", maskToken(cfg.Token)),
		fmt.Sprintf("Изменить адрес сервера  [%s]", cfg.ServerURL),
		"Назад",
	}
	for i, it := range items {
		if i == selected {
			fmt.Println(highlightLineSelected(" " + it + " "))
		} else {
			fmt.Println(" " + it)
		}
	}
	fmt.Println()
	fmt.Println(dim + "↑ ↓ — навигация, Enter — изменить/выбрать, q/Esc — назад" + reset)
	if status != "" {
		fmt.Println()
		fmt.Println(status)
	}
}

// promptLine временно закрывает raw-режим клавиатуры и читает строку из stdin
// Возвращает (значение, было_ли_изменение, ошибка)
func promptLine(prompt string, reader *bufio.Reader) (string, bool, error) {
	// Закрываем raw-режим
	_ = keyboard.Close()
	fmt.Print(prompt)
	text, err := reader.ReadString('\n')
	openErr := keyboard.Open()
	if openErr != nil {
		return "", false, openErr
	}
	if err != nil && err != io.EOF {
		return "", false, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

func expandPath(p string) string {
	p = strings.TrimSpace(os.ExpandEnv(p))
	if p == "" {
		return p
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~\\") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	u = strings.TrimRight(u, "/")
	if _, err := url.ParseRequestURI(u); err != nil {
		return ""
	}
	return u
}
