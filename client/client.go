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
	"os"
	"path/filepath"
	"strings"
)

// const serverURL = "http://syncerch.meysner.ru"
const serverURL = "http://localhost:1244"
const configFile = "config.json"

type Config struct {
	Token      string `json:"token"`
	FolderPath string `json:"folder_path"`
}

func main() {
	config := loadConfig()

	reader := bufio.NewReader(os.Stdin)

	if config.Token == "" {
		fmt.Print("Введите токен: ")
		token, _ := reader.ReadString('\n')
		config.Token = strings.TrimSpace(token)
	}

	if config.FolderPath == "" {
		fmt.Print("Введите путь к папке: ")
		folderPath, _ := reader.ReadString('\n')
		config.FolderPath = strings.TrimSpace(folderPath)
	}

	saveConfig(config)

	for {
		fmt.Println("\nВыберите действие:")
		fmt.Println("1 - Загрузить папку на сервер")
		fmt.Println("2 - Скачать папку с сервера")
		fmt.Print("> ")

		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)

		switch choice {
		case "1":
			if err := uploadFolder(config.Token, config.FolderPath); err != nil {
				fmt.Println("Ошибка загрузки:", err)
			} else {
				fmt.Println("Папка успешно загружена")
			}
		case "2":
			if err := downloadFolder(config.Token, config.FolderPath); err != nil {
				fmt.Println("Ошибка скачивания:", err)
			} else {
				fmt.Println("Папка успешно скачана")
			}
		default:
			fmt.Println("Неизвестная команда")
		}
	}
}

func loadConfig() Config {
	var cfg Config
	file, err := os.Open(configFile)
	if err != nil {
		return cfg // если файла нет — возвращаем пустую конфигурацию
	}
	defer file.Close()
	json.NewDecoder(file).Decode(&cfg)
	return cfg
}

func saveConfig(cfg Config) {
	file, err := os.Create(configFile)
	if err != nil {
		fmt.Println("Не удалось сохранить конфигурацию:", err)
		return
	}
	defer file.Close()
	json.NewEncoder(file).Encode(cfg)
}

func uploadFolder(token, folderPath string) error {
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
	io.Copy(part, file)
	writer.Close()

	req, err := http.NewRequest("POST", serverURL+"/upload", body)
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

func downloadFolder(token, folderPath string) error {
	req, err := http.NewRequest("GET", serverURL+"/download", nil)
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
	io.Copy(out, resp.Body)
	out.Close()
	defer os.Remove(tmpZip)

	os.RemoveAll(folderPath)
	os.MkdirAll(folderPath, 0755)
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

	filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name, _ = filepath.Rel(src, path)

		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(writer, file)
			return err
		}
		return nil
	})
	return nil
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, f.Mode())
			continue
		}
		if err = os.MkdirAll(filepath.Dir(fpath), f.Mode()); err != nil {
			return err
		}
		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
