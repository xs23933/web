package web

// Handler 基础分类
type Handler struct {
	prefix string
}

// SetPrefix 设置前缀
func (h *Handler) SetPrefix(prefix string) {
	h.prefix = prefix
}

// Preload 预处理使用 必须配置 Next结尾
func (h *Handler) Preload(c *Ctx) {
	c.Next()
}

// Init 初始化操作
func (h *Handler) Init() {}

// Prefix 初始化操作
func (h *Handler) Prefix() string {
	return h.prefix
}

type handle interface {
	// 获得前缀
	Prefix() string
	// 初始化
	Init()
	// 每次调用时预处理
	Preload(*Ctx)
}
