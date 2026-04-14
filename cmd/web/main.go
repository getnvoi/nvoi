package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

var md = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Typographer,
		highlighting.NewHighlighting(
			highlighting.WithCustomStyle(styles.Get("monokailight")),
			highlighting.WithFormatOptions(
				chromahtml.WithClasses(true),
			),
		),
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
	),
)

var funcMap = template.FuncMap{
	"formatDate": func(s string) string {
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return s
		}
		return t.Format("January 2, 2006")
	},
}

var templates map[string]*template.Template

// --- Content types ---

type PageMeta struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Date        string `yaml:"date"`
	Author      string `yaml:"author"`
	Draft       bool   `yaml:"draft"`
	Order       int    `yaml:"order"`
}

type ContentPage struct {
	Meta    PageMeta
	Slug    string
	Content template.HTML
}

func main() {
	if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: dsn}); err != nil {
			slog.Warn("sentry init failed", "error", err)
		} else {
			defer sentry.Flush(2 * time.Second)
		}
	}

	root := findRoot()
	assets := loadManifest(root)
	templates = loadTemplates(root)
	contentDir := filepath.Join(root, "cmd/web/content")

	r := gin.Default()
	r.Use(sentrygin.New(sentrygin.Options{Repanic: true}))

	// 301 www → naked domain (preserve path)
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.Host, "www.") {
			naked := strings.TrimPrefix(c.Request.Host, "www.")
			c.Redirect(http.StatusMovedPermanently, "https://"+naked+c.Request.RequestURI)
			c.Abort()
			return
		}
		c.Next()
	})

	r.Static("/static", filepath.Join(root, "cmd/web/static"))

	// Homepage
	r.GET("/", homeHandler(assets))

	// Blog
	r.GET("/blog", blogListHandler(contentDir, assets))
	r.GET("/blog/:slug", blogPostHandler(contentDir, assets))

	// Static pages
	for _, slug := range []string{"privacy", "terms", "contact"} {
		r.GET("/"+slug, pageHandler(contentDir, slug, assets))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	r.Run(":" + port)
}

// --- Handlers ---

func homeHandler(assets map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		renderTemplate(c, "home.html", gin.H{
			"title": "Deploy containers to cloud servers",
			"css":   assets["output.css"],
		})
	}
}

func blogListHandler(contentDir string, assets map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		posts, _ := listContent(filepath.Join(contentDir, "blog"))
		renderTemplate(c, "blog.html", gin.H{
			"title": "Blog",
			"css":   assets["output.css"],
			"posts": posts,
		})
	}
}

func blogPostHandler(contentDir string, assets map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		slug := filepath.Base(c.Param("slug"))
		if strings.Contains(slug, "..") || slug == "." {
			c.Status(http.StatusNotFound)
			return
		}
		page, err := parseContent(filepath.Join(contentDir, "blog", slug+".md"))
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		renderTemplate(c, "page.html", gin.H{
			"title": page.Meta.Title,
			"css":   assets["output.css"],
			"page":  page,
		})
	}
}

func pageHandler(contentDir, slug string, assets map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, err := parseContent(filepath.Join(contentDir, slug+".md"))
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		renderTemplate(c, "page.html", gin.H{
			"title": page.Meta.Title,
			"css":   assets["output.css"],
			"page":  page,
		})
	}
}

// --- Content helpers ---

func parseContent(path string) (*ContentPage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var meta PageMeta
	body := data

	if bytes.HasPrefix(data, []byte("---\n")) {
		if idx := bytes.Index(data[4:], []byte("\n---\n")); idx >= 0 {
			if err := yaml.Unmarshal(data[4:4+idx], &meta); err != nil {
				return nil, err
			}
			body = data[4+idx+5:]
		}
	}

	var buf bytes.Buffer
	if err := md.Convert(body, &buf); err != nil {
		return nil, err
	}

	slug := strings.TrimSuffix(filepath.Base(path), ".md")

	return &ContentPage{
		Meta:    meta,
		Slug:    slug,
		Content: template.HTML(buf.String()),
	}, nil
}

func listContent(dir string) ([]ContentPage, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}

	var pages []ContentPage
	for _, f := range files {
		if strings.HasPrefix(filepath.Base(f), "_") {
			continue
		}
		page, err := parseContent(f)
		if err != nil {
			continue
		}
		if page.Meta.Draft {
			continue
		}
		pages = append(pages, *page)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Meta.Date > pages[j].Meta.Date
	})

	return pages, nil
}

// --- Template infrastructure ---

func renderTemplate(c *gin.Context, name string, data gin.H) {
	t, ok := templates[name]
	if !ok {
		c.Status(http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	c.Writer.Write(buf.Bytes())
}

func loadTemplates(root string) map[string]*template.Template {
	dir := filepath.Join(root, "cmd/web/templates")
	layout := filepath.Join(dir, "layout.html")

	pages, _ := filepath.Glob(filepath.Join(dir, "*.html"))
	tmpl := make(map[string]*template.Template)
	for _, page := range pages {
		name := filepath.Base(page)
		if name == "layout.html" {
			continue
		}
		tmpl[name] = template.Must(
			template.New(name).Funcs(funcMap).ParseFiles(layout, page),
		)
	}
	return tmpl
}

func loadManifest(root string) map[string]string {
	fallback := map[string]string{"output.css": "/static/css/output.css"}
	data, err := os.ReadFile(filepath.Join(root, "cmd/web/static/manifest.json"))
	if err != nil {
		return fallback
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return fallback
	}
	return m
}

func findRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
