package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gorilla/mux"
	"github.com/op/go-logging"
)

// 鉴权用的KEY
const SYSTEMID = ""

// 目标文件名称
const FILENAME = "test.txt"

var log = logging.MustGetLogger("rename")

// 日志打印格式
var format = logging.MustStringFormatter(
	"%{shortfile} %{time:2006-01-02T15:04:05} %{level:.1s} %{id:04d} %{shortfunc} %{message}",
)

// 事业部更名接口入参JSON
type BodyJson struct {
	Department DateJson `json:"department"`
	Rename     DateJson `json:"rename"`
}

// 事业部更名接口返回JSON
type ResultJson struct {
	Data    DateJson `json:"data"`
	Message string   `json:"message"`
	Status  string   `json:"status"`
}

// 事业部列表JSON
type DateJson struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

// IAC鉴权接口返回的JSON格式
type IacJson struct {
	Status  int    `json:"status"`
	ErrCode int    `json:"errCode"`
	ErrMsg  string `json:"errMsg"`
	// Data    string `json:"data"`
}

// 初始化日志文件
func init() {
	logFile, err := os.OpenFile("rename.log", os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		log.Error(err)
	}
	backend1 := logging.NewLogBackend(os.Stderr, "", 0)
	backend2 := logging.NewLogBackend(logFile, "", 0)
	backend2Formatter := logging.NewBackendFormatter(backend2, format)
	backend1Leveled := logging.AddModuleLevel(backend1)
	backend1Leveled.SetLevel(logging.INFO, "")
	logging.SetBackend(backend1Leveled, backend2Formatter)
}

// 主程序入口
// 交叉编译:GOOS=linux GOARCH=amd64 go build
func main() {
	router := mux.NewRouter().StrictSlash(true)
	// 监听端口
	router.HandleFunc("/v1/department/renamed", renameController)
	router.HandleFunc("/v1/department/{department}", searchController)
	err := http.ListenAndServe(":9082", router)
	if err != nil {
		log.Error(err)
	}
}

// 事业部名称查询接口
func searchController(w http.ResponseWriter, r *http.Request) {
	// IAC鉴权
	if !iacValid(w, r) {
		return
	}
	vars := mux.Vars(r)
	department := vars["department"]
	// 返回失败结果时的JSON
	errDate := DateJson{Id: "", Name: ""}
	// 获取当前可执行文件目录，查找当前目录下MappingProfile.xml
	rootPath, err := getCurrentPath()
	if err != nil {
		log.Error(err)
		result := ResultJson{Message: err.Error(), Status: "200", Data: errDate}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	file := rootPath + FILENAME
	log.Infof("文件路径:%s", file)
	// 查找事业部
	result, err := findFile(file, department)
	if err != nil {
		log.Error(err)
		result := ResultJson{Message: err.Error(), Status: "200", Data: errDate}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if result {
		date := DateJson{Id: "", Name: department}
		result := ResultJson{Message: "success", Status: "200", Data: date}
		_ = json.NewEncoder(w).Encode(result)
		return
	} else {
		result := ResultJson{Message: department + " 不存在", Status: "200", Data: errDate}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
}

// 事业部更名接口
func renameController(w http.ResponseWriter, r *http.Request) {
	// IAC鉴权
	if !iacValid(w, r) {
		return
	}
	// 返回失败结果时的JSON
	errDate := DateJson{Id: "", Name: ""}
	// 判断是否为PUT请求，处理请求
	if r.Method == "PUT" {
		if r.Body == nil {
			result := ResultJson{Message: "请求中body为空", Status: "200", Data: errDate}
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		// 解析body 获取变更前事业部、变更后事业部
		var body BodyJson
		_ = json.NewDecoder(r.Body).Decode(&body)
		origin := body.Department.Name
		target := body.Rename.Name
		if origin != "" && target != "" {
			// 执行事业部更名操作
			rename(origin, target)
			date := DateJson{Id: "", Name: target}
			result := ResultJson{Message: "success", Status: "200", Data: date}
			// 返回结果
			_ = json.NewEncoder(w).Encode(result)
			return
		} else {
			result := ResultJson{Message: "请求中body为空", Status: "200", Data: errDate}
			_ = json.NewEncoder(w).Encode(result)
			return
		}
	} else {
		result := ResultJson{Message: "只支持PUT请求", Status: "200", Data: errDate}
		_ = json.NewEncoder(w).Encode(result)
		return
	}
}

// IAC鉴权
func iacValid(w http.ResponseWriter, r *http.Request) bool {
	// 打印url，获取token
	log.Infof("url:%s, method:%s", r.URL, r.Method)
	var token string
	if len(r.Header) > 0 {
		for k, v := range r.Header {
			if strings.ToLower(k) == "x-iac-token" {
				token = v[0]
			}
		}
	}
	// 返回失败结果时的JSON
	errDate := DateJson{Id: "", Name: ""}
	// IAC鉴权
	if token != "" {
		if !httpGet(token) {
			result := ResultJson{Message: "鉴权失败", Status: "200", Data: errDate}
			_ = json.NewEncoder(w).Encode(result)
			return false
		}
	} else {
		result := ResultJson{Message: "header中找不到x-iac-token", Status: "200", Data: errDate}
		_ = json.NewEncoder(w).Encode(result)
		return false
	}
	log.Infof("鉴权成功, token:%s", token)
	return true
}

// 执行事业部更名
func rename(origin, target string) {
	// 获取当前可执行文件目录，查找当前目录下目标文件
	rootPath, err := getCurrentPath()
	file := rootPath + FILENAME
	log.Infof("文件路径:%s", file)
	// 判断是否需要执行替换
	output, needHandle, err := readFile(file, origin, target)
	if err != nil {
		panic(err)
	}
	if needHandle {
		err = writeToFile(file, output)
		if err != nil {
			panic(err)
		}
		log.Info("执行rename完成，替换成功")
	} else {
		log.Info("执行rename完成，不需要替换")
	}
}

// 读取文件，判断是否需要更名
func readFile(filePath string, origin string, target string) ([]byte, bool, error) {
	f, err := os.OpenFile(filePath, os.O_RDONLY, 0644)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	needHandle := false
	output := make([]byte, 0)
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				return output, needHandle, nil
			}
			return nil, needHandle, err
		}
		// 例如:供应链事业部-> TV供应链事业部
		// 只替换以下4种情况，防止"整机供应链事业部"相识事业部被误替换
		// "供应链事业部" -> "TV供应链事业部"
		// "供应链事业部, -> "TV供应链事业部,
		// ,供应链事业部, -> ,TV供应链事业部,
		// ,供应链事业部" -> ,TV供应链事业部"
		pattern := "[,\"]" + origin + "[,\"]"
		replaceFunc := func(s []byte) []byte {
			tempReg := regexp.MustCompile(origin)
			newSrc := tempReg.ReplaceAll(s, []byte(target))
			return newSrc
		}
		if ok, _ := regexp.Match(pattern, line); ok {
			reg := regexp.MustCompile(pattern)
			newByte := reg.ReplaceAllFunc(line, replaceFunc)
			output = append(output, newByte...)
			output = append(output, []byte("\r\n")...)
			if !needHandle {
				needHandle = true
			}
		} else {
			output = append(output, line...)
			output = append(output, []byte("\r\n")...)
		}
	}
}

// 查找文件，判断是否需要更名
func findFile(filePath string, origin string) (bool, error) {
	f, err := os.OpenFile(filePath, os.O_RDONLY, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	existFlag := false
	for {
		line, _, err := reader.ReadLine()
		if err != nil {
			if err == io.EOF {
				return existFlag, nil
			}
			return existFlag, err
		}
		pattern := "[,\"]" + origin + "[,\"]"
		if ok, _ := regexp.Match(pattern, line); ok {
			existFlag = true
			return existFlag, nil
		}
	}
}

// 执行替换操作
func writeToFile(filePath string, outPut []byte) error {
	f, err := os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, 0644)
	defer f.Close()
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(f)
	_, err = writer.Write(outPut)
	if err != nil {
		return err
	}
	_ = writer.Flush()
	return nil
}

// 获取当前可执行文件目录
func getCurrentPath() (string, error) {
	file, err := exec.LookPath(os.Args[0])
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(path, "/")
	if i < 0 {
		i = strings.LastIndex(path, "\\")
	}
	if i < 0 {
		return "", errors.New("can not find '/' or '\\'")
	}
	return string(path[0 : i+1]), nil
}

// IAC鉴权
func httpGet(token string) bool {
	// 调用鉴权服务
	url := "test" + SYSTEMID + "&token=" + token
	resp, err := http.Get(url)
	if err != nil {
		log.Errorf("http.Get has error url:%s, err:", url, err)
	}
	var iacJson IacJson
	if resp.Body == nil {
		return false
	}
	_ = json.NewDecoder(resp.Body).Decode(&iacJson)
	log.Infof("鉴权结果：%s", iacJson)
	if iacJson.Status == 200 {
		return true
	} else {
		return false
	}
}
