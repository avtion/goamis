package main

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/boltdb/bolt"
	"github.com/gin-gonic/gin"
)

const (
	defaultIndex = "index"
	defaultPort  = "80"
)

//go:embed static
var systemStatic embed.FS

// bolt 是一款适合读取密集型工作的内嵌式数据库
var (
	boltDB        *bolt.DB
	defaultBucket = []byte("page")
)

var page404Data = []byte(`{"type":"page","title":"404","body":[{"type":"markdown","value":"# 🚫 Oops,  找不到对应的页面配置\n[👉 点击我返回页面列表](/)"}],"regions":["body"]}`)

type (
	basicResp struct {
		Status int                    `json:"status"`
		Msg    string                 `json:"msg"`
		Data   map[string]interface{} `json:"data"`
	}
	pageItem struct {
		Name   string `json:"name" validate:"required"`
		Config string `json:"config" validate:"required,json"`
	}
)

// 初始化内嵌式数据库
func initBoltDB() {
	var err error
	boltDB, err = bolt.Open("amis.db", 0600, nil)
	if err != nil {
		log.Fatalln(err)
		return
	}

	// 从 static 文件夹中获取全部的页面配置并写入数据库
	_ = boltDB.Batch(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(defaultBucket)
		if err != nil {
			return err
		}

		_ = fs.WalkDir(systemStatic, "static", func(path string, d fs.DirEntry, err error) error {
			// check if ext is json
			if d.IsDir() {
				return nil
			}
			_, filename := filepath.Split(path)
			fileExt := filepath.Ext(filename)
			if fileExt != ".json" {
				return nil
			}

			// write page data to db
			pageData, err := systemStatic.ReadFile(path)
			if err != nil {
				log.Printf("read page data failed, filename: %s, err: %v\n", filename, err)
				return nil
			}
			if err := bucket.Put([]byte(strings.TrimSuffix(filename, fileExt)), pageData); err != nil {
				log.Printf("failed to write page data to bolt db, err: %v\n", err)
				return nil
			}
			log.Printf("load page data to bolt db, file: %s\n", filename)
			return nil
		})
		return nil
	})
}

func main() {
	initBoltDB()

	tmplFs, err := fs.Sub(systemStatic, "static")
	if err != nil {
		log.Fatalln(err)
		return
	}
	tmpl, err := template.ParseFS(tmplFs, "*.tmpl")
	if err != nil {
		log.Fatalln(err)
		return
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = defaultPort
	}

	engine := gin.Default()
	engine.SetHTMLTemplate(tmpl)

	// 首页直接跳转到默认页面
	engine.GET("/", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/page/"+defaultIndex) })
	engine.GET("/page/:name", renderPage)

	// 页面配置
	engine.GET("/config/list", listConfig)
	engine.GET("/config/get/:name", getConfig)
	engine.GET("/config/delete/:name", deleteConfig)
	engine.POST("/config/save", saveConfig)
	if err := engine.Run(":" + port); err != nil {
		log.Fatalln(err)
		return
	}
}

func renderPage(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		name = "404"
	}
	c.HTML(http.StatusOK, "amis.tmpl", gin.H{
		"pageTitle":     name,
		"pageSchemaApi": "GET:/config/get/" + name,
		"getConfigAddr": "/config/get/" + name,
	})
}

func listConfig(c *gin.Context) {
	var pages []*pageItem
	if err := boltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(defaultBucket)
		return bucket.ForEach(func(k, v []byte) error {
			pages = append(pages, &pageItem{Name: string(k), Config: string(v)})
			return nil
		})
	}); err != nil {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, &basicResp{Status: 0, Data: map[string]interface{}{
		"items": pages,
		"total": len(pages),
	}})
}

func getConfig(c *gin.Context) {
	var name = c.Param("name")
	if name == "" {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: "name is empty"})
		return
	}
	var pageData []byte
	_ = boltDB.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(defaultBucket)
		pageData = bucket.Get([]byte(name))
		return nil
	})
	// 404
	if len(pageData) == 0 {
		pageData = page404Data
	}
	c.Data(http.StatusOK, "application/json", pageData)
}

func deleteConfig(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: "name is empty"})
		return
	}
	if err := boltDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(defaultBucket)
		return bucket.Delete([]byte(name))
	}); err != nil {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, &basicResp{Status: 0, Msg: "delete page config successfully"})
}

func saveConfig(c *gin.Context) {
	var req = new(pageItem)
	if err := c.ShouldBindJSON(req); err != nil {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: err.Error()})
		return
	}
	if err := boltDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(defaultBucket)
		return bucket.Put([]byte(req.Name), []byte(req.Config))
	}); err != nil {
		c.AbortWithStatusJSON(http.StatusOK, &basicResp{Status: -1, Msg: err.Error()})
		return
	}
	c.JSON(http.StatusOK, &basicResp{Status: 0, Msg: "save page config successfully"})
}
