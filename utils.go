package web

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"unsafe"
)

func isChild() bool {
	for i := range os.Args[1:] {
		if os.Args[1:][i] == "-child" {
			return true
		}
	}
	return false
}

// Adapted from:
// https://github.com/jshttp/fresh/blob/10e0471669dbbfbfd8de65bc6efac2ddd0bfa057/index.js#L110
func parseTokenList(noneMatchBytes []byte) []string {
	var (
		start int
		end   int
		list  []string
	)
	for i := range noneMatchBytes {
		switch noneMatchBytes[i] {
		case 0x20:
			if start == end {
				start = i + 1
				end = i + 1
			}
		case 0x2c:
			list = append(list, getString(noneMatchBytes[start:end]))
			start = i + 1
			end = i + 1
		default:
			end = i + 1
		}
	}

	list = append(list, getString(noneMatchBytes[start:end]))
	return list
}

// #nosec G103
// getBytes converts string to a byte slice without memory allocation.
// See https://groups.google.com/forum/#!msg/Golang-Nuts/ENgbUzYvCuU/90yGx7GUAgAJ .
var getBytes = func(s string) (b []byte) {
	return *(*[]byte)(unsafe.Pointer(&s))
}

var getString = func(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func getParams(path string) (params []string) {
	if len(path) < 1 {
		return
	}
	segments := strings.Split(path, "/")
	replacer := strings.NewReplacer(":", "", "?", "")
	for i := range segments {
		s := segments[i]
		if s == "" {
			continue
		} else if s[0] == ':' {
			params = append(params, replacer.Replace(s))
		}
		if strings.Contains(s, "*") {
			params = append(params, "*")
		}
	}
	return
}

func getRegex(path string) (*regexp.Regexp, error) {
	pattern := "^"
	segments := strings.Split(path, "/")
	for i := range segments {
		s := segments[i]
		if s == "" {
			continue
		}
		if s[0] == ':' {
			if strings.Contains(s, "?") {
				pattern += "(?:/([^/]+?))?"
			} else {
				pattern += "/(?:([^/]+?))"
			}
		} else if s[0] == '*' {
			pattern += "/(.*)"
		} else {
			pattern += "/" + s
		}
	}
	pattern += "/?$"
	regex, err := regexp.Compile(pattern)
	return regex, err
}

func setETag(ctx *Ctx, body []byte, weak bool) {
	if len(body) <= 0 {
		return
	}
	clientETag := ctx.Get("If-None-Match")
	crc332q := crc32.MakeTable(0xD5828281)
	etag := fmt.Sprintf(`"%d-%v"`, len(body), crc32.Checksum(body, crc332q))
	if weak {
		etag = fmt.Sprintf(`W/"%s"`, etag)
	}

	if strings.HasPrefix(clientETag, "W/") {
		if clientETag[2:] == etag || clientETag[2:] == etag[2:] {
			ctx.SendStatus(304)
			ctx.ResetBody()
			return
		}
		ctx.Set("ETag", etag)
		return
	}
	if strings.Contains(clientETag, etag) {
		ctx.SendStatus(304)
		ctx.ResetBody()
		return
	}
	ctx.Set("ETag", etag)
}

// HTTP status codes were copied from net/http.
var statusMessages = map[int]string{
	100: "Continue",
	101: "Switching Protocols",
	102: "Processing",
	103: "Early Hints",
	200: "OK",
	201: "Created",
	202: "Accepted",
	203: "Non-Authoritative Information",
	204: "No Content",
	205: "Reset Content",
	206: "Partial Content",
	207: "Multi-Status",
	208: "Already Reported",
	226: "IM Used",
	300: "Multiple Choices",
	301: "Moved Permanently",
	302: "Found",
	303: "See Other",
	304: "Not Modified",
	305: "Use Proxy",
	306: "Switch Proxy",
	307: "Temporary Redirect",
	308: "Permanent Redirect",
	400: "Bad Request",
	401: "Unauthorized",
	402: "Payment Required",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	406: "Not Acceptable",
	407: "Proxy Authentication Required",
	408: "Request Timeout",
	409: "Conflict",
	410: "Gone",
	411: "Length Required",
	412: "Precondition Failed",
	413: "Request Entity Too Large",
	414: "Request URI Too Long",
	415: "Unsupported Media Type",
	416: "Requested Range Not Satisfiable",
	417: "Expectation Failed",
	418: "I'm a teapot",
	421: "Misdirected Request",
	422: "Unprocessable Entity",
	423: "Locked",
	424: "Failed Dependency",
	426: "Upgrade Required",
	428: "Precondition Required",
	429: "Too Many Requests",
	431: "Request Header Fields Too Large",
	451: "Unavailable For Legal Reasons",
	500: "Internal Server Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Gateway Timeout",
	505: "HTTP Version Not Supported",
	506: "Variant Also Negotiates",
	507: "Insufficient Storage",
	508: "Loop Detected",
	510: "Not Extended",
	511: "Network Authentication Required",
}

// methodINT HTTP methods and their unique INTs.
var methodINT = map[string]int{
	MethodGet:     0,
	MethodHead:    1,
	MethodPost:    2,
	MethodPut:     3,
	MethodDelete:  4,
	MethodConnect: 5,
	MethodOptions: 6,
	MethodTrace:   7,
	MethodPatch:   8,
}

// HTTP methods were copied from net/http.
const (
	MethodGet     = "GET"     // RFC 7231, 4.3.1
	MethodHead    = "HEAD"    // RFC 7231, 4.3.2
	MethodPost    = "POST"    // RFC 7231, 4.3.3
	MethodPut     = "PUT"     // RFC 7231, 4.3.4
	MethodPatch   = "PATCH"   // RFC 5789
	MethodDelete  = "DELETE"  // RFC 7231, 4.3.5
	MethodConnect = "CONNECT" // RFC 7231, 4.3.6
	MethodOptions = "OPTIONS" // RFC 7231, 4.3.7
	MethodTrace   = "TRACE"   // RFC 7231, 4.3.8
)

func fixURI(pre, src, tag string) string {
	uri := path.Join(pre, strings.TrimLeft(src, tag))
	if len(uri) == 0 {
		uri = "/"
	}
	return uri
}

// 名称替换
func newSafeMap() *safeMap {
	return &safeMap{l: new(sync.RWMutex), m: make(map[string]string)}
}

var smap = newSafeMap()

type safeMap struct {
	m map[string]string
	l *sync.RWMutex
}

func (s *safeMap) Set(key string, value string) {
	s.l.Lock()
	defer s.l.Unlock()
	s.m[key] = value
}

func (s *safeMap) Get(key string) string {
	s.l.RLock()
	defer s.l.RUnlock()
	return s.m[key]
}

var commonInitialisms = []string{"API", "ASCII", "CPU", "CSS", "DNS", "EOF", "GUID", "HTML", "HTTP", "HTTPS", "ID", "IP", "JSON", "LHS", "QPS", "RAM", "RHS", "RPC", "SLA", "SMTP", "SSH", "TLS", "TTL", "UID", "UI", "UUID", "URI", "URL", "UTF8", "VM", "XML", "XSRF", "XSS"}
var commonInitialismsReplacer *strings.Replacer

func init() {
	var commonInitialismsForReplacer []string
	for _, initialism := range commonInitialisms {
		commonInitialismsForReplacer = append(commonInitialismsForReplacer, initialism, strings.Title(strings.ToLower(initialism)))
	}
	commonInitialismsReplacer = strings.NewReplacer(commonInitialismsForReplacer...)
}

func toNamer(name string) string {
	const (
		lower = false
		upper = true
	)

	if name == "" {
		return ""
	}

	var (
		value                                    = commonInitialismsReplacer.Replace(name)
		buf                                      = bytes.NewBufferString("")
		lastCase, currCase, nextCase, nextNumber bool
	)

	for i, v := range value[:len(value)-1] {
		nextCase = bool(value[i+1] >= 'A' && value[i+1] <= 'Z')
		nextNumber = bool(value[i+1] >= '0' && value[i+1] <= '9')

		if i > 0 {
			if currCase == upper {
				if lastCase == upper && (nextCase == upper || nextNumber == upper) {
					buf.WriteRune(v)
				} else {
					if value[i-1] != '/' && value[i+1] != '/' {
						buf.WriteRune('/')
					}
					buf.WriteRune(v)
				}
			} else {
				buf.WriteRune(v)
				if i == len(value)-2 && (nextCase == upper && nextNumber == lower) {
					buf.WriteRune('/')
				}
			}
		} else {
			currCase = upper
			buf.WriteRune(v)
		}
		lastCase = currCase
		currCase = nextCase
	}

	buf.WriteByte(value[len(value)-1])

	s := strings.ToLower(buf.String())
	smap.Set(name, s)
	reps := []string{"_", "/:", "params", ":param?", "param", ":param"}
	replacer := strings.NewReplacer(reps...)
	s = replacer.Replace(s)
	return s
}

// MIME types were copied from labstack/echo
const (
	MIMETextXML   = "text/xml"
	MIMETextHTML  = "text/html"
	MIMETextPlain = "text/plain"

	MIMEApplicationJSON       = "application/json"
	MIMEApplicationJavaScript = "application/javascript"
	MIMEApplicationXML        = "application/xml"
	MIMEApplicationForm       = "application/x-www-form-urlencoded"

	MIMEMultipartForm = "multipart/form-data"

	MIMEOctetStream = "application/octet-stream"
)

// MIME types were copied from nginx/mime.types.
var extensionMIME = map[string]string{
	// without dot
	"html":    "text/html",
	"htm":     "text/html",
	"shtml":   "text/html",
	"css":     "text/css",
	"gif":     "image/gif",
	"jpeg":    "image/jpeg",
	"jpg":     "image/jpeg",
	"xml":     "application/xml",
	"js":      "application/javascript",
	"atom":    "application/atom+xml",
	"rss":     "application/rss+xml",
	"mml":     "text/mathml",
	"txt":     "text/plain",
	"jad":     "text/vnd.sun.j2me.app-descriptor",
	"wml":     "text/vnd.wap.wml",
	"htc":     "text/x-component",
	"png":     "image/png",
	"svg":     "image/svg+xml",
	"svgz":    "image/svg+xml",
	"tif":     "image/tiff",
	"tiff":    "image/tiff",
	"wbmp":    "image/vnd.wap.wbmp",
	"webp":    "image/webp",
	"ico":     "image/x-icon",
	"jng":     "image/x-jng",
	"bmp":     "image/x-ms-bmp",
	"woff":    "font/woff",
	"woff2":   "font/woff2",
	"jar":     "application/java-archive",
	"war":     "application/java-archive",
	"ear":     "application/java-archive",
	"json":    "application/json",
	"hqx":     "application/mac-binhex40",
	"doc":     "application/msword",
	"pdf":     "application/pdf",
	"ps":      "application/postscript",
	"eps":     "application/postscript",
	"ai":      "application/postscript",
	"rtf":     "application/rtf",
	"m3u8":    "application/vnd.apple.mpegurl",
	"kml":     "application/vnd.google-earth.kml+xml",
	"kmz":     "application/vnd.google-earth.kmz",
	"xls":     "application/vnd.ms-excel",
	"eot":     "application/vnd.ms-fontobject",
	"ppt":     "application/vnd.ms-powerpoint",
	"odg":     "application/vnd.oasis.opendocument.graphics",
	"odp":     "application/vnd.oasis.opendocument.presentation",
	"ods":     "application/vnd.oasis.opendocument.spreadsheet",
	"odt":     "application/vnd.oasis.opendocument.text",
	"wmlc":    "application/vnd.wap.wmlc",
	"7z":      "application/x-7z-compressed",
	"cco":     "application/x-cocoa",
	"jardiff": "application/x-java-archive-diff",
	"jnlp":    "application/x-java-jnlp-file",
	"run":     "application/x-makeself",
	"pl":      "application/x-perl",
	"pm":      "application/x-perl",
	"prc":     "application/x-pilot",
	"pdb":     "application/x-pilot",
	"rar":     "application/x-rar-compressed",
	"rpm":     "application/x-redhat-package-manager",
	"sea":     "application/x-sea",
	"swf":     "application/x-shockwave-flash",
	"sit":     "application/x-stuffit",
	"tcl":     "application/x-tcl",
	"tk":      "application/x-tcl",
	"der":     "application/x-x509-ca-cert",
	"pem":     "application/x-x509-ca-cert",
	"crt":     "application/x-x509-ca-cert",
	"xpi":     "application/x-xpinstall",
	"xhtml":   "application/xhtml+xml",
	"xspf":    "application/xspf+xml",
	"zip":     "application/zip",
	"bin":     "application/octet-stream",
	"exe":     "application/octet-stream",
	"dll":     "application/octet-stream",
	"deb":     "application/octet-stream",
	"dmg":     "application/octet-stream",
	"iso":     "application/octet-stream",
	"img":     "application/octet-stream",
	"msi":     "application/octet-stream",
	"msp":     "application/octet-stream",
	"msm":     "application/octet-stream",
	"mid":     "audio/midi",
	"midi":    "audio/midi",
	"kar":     "audio/midi",
	"mp3":     "audio/mpeg",
	"ogg":     "audio/ogg",
	"m4a":     "audio/x-m4a",
	"ra":      "audio/x-realaudio",
	"3gpp":    "video/3gpp",
	"3gp":     "video/3gpp",
	"ts":      "video/mp2t",
	"mp4":     "video/mp4",
	"mpeg":    "video/mpeg",
	"mpg":     "video/mpeg",
	"mov":     "video/quicktime",
	"webm":    "video/webm",
	"flv":     "video/x-flv",
	"m4v":     "video/x-m4v",
	"mng":     "video/x-mng",
	"asx":     "video/x-ms-asf",
	"asf":     "video/x-ms-asf",
	"wmv":     "video/x-ms-wmv",
	"avi":     "video/x-msvideo",

	// with dot
	".html":    "text/html",
	".htm":     "text/html",
	".shtml":   "text/html",
	".css":     "text/css",
	".gif":     "image/gif",
	".jpeg":    "image/jpeg",
	".jpg":     "image/jpeg",
	".xml":     "application/xml",
	".js":      "application/javascript",
	".atom":    "application/atom+xml",
	".rss":     "application/rss+xml",
	".mml":     "text/mathml",
	".txt":     "text/plain",
	".jad":     "text/vnd.sun.j2me.app-descriptor",
	".wml":     "text/vnd.wap.wml",
	".htc":     "text/x-component",
	".png":     "image/png",
	".svg":     "image/svg+xml",
	".svgz":    "image/svg+xml",
	".tif":     "image/tiff",
	".tiff":    "image/tiff",
	".wbmp":    "image/vnd.wap.wbmp",
	".webp":    "image/webp",
	".ico":     "image/x-icon",
	".jng":     "image/x-jng",
	".bmp":     "image/x-ms-bmp",
	".woff":    "font/woff",
	".woff2":   "font/woff2",
	".jar":     "application/java-archive",
	".war":     "application/java-archive",
	".ear":     "application/java-archive",
	".json":    "application/json",
	".hqx":     "application/mac-binhex40",
	".doc":     "application/msword",
	".pdf":     "application/pdf",
	".ps":      "application/postscript",
	".eps":     "application/postscript",
	".ai":      "application/postscript",
	".rtf":     "application/rtf",
	".m3u8":    "application/vnd.apple.mpegurl",
	".kml":     "application/vnd.google-earth.kml+xml",
	".kmz":     "application/vnd.google-earth.kmz",
	".xls":     "application/vnd.ms-excel",
	".eot":     "application/vnd.ms-fontobject",
	".ppt":     "application/vnd.ms-powerpoint",
	".odg":     "application/vnd.oasis.opendocument.graphics",
	".odp":     "application/vnd.oasis.opendocument.presentation",
	".ods":     "application/vnd.oasis.opendocument.spreadsheet",
	".odt":     "application/vnd.oasis.opendocument.text",
	".wmlc":    "application/vnd.wap.wmlc",
	".7z":      "application/x-7z-compressed",
	".cco":     "application/x-cocoa",
	".jardiff": "application/x-java-archive-diff",
	".jnlp":    "application/x-java-jnlp-file",
	".run":     "application/x-makeself",
	".pl":      "application/x-perl",
	".pm":      "application/x-perl",
	".prc":     "application/x-pilot",
	".pdb":     "application/x-pilot",
	".rar":     "application/x-rar-compressed",
	".rpm":     "application/x-redhat-package-manager",
	".sea":     "application/x-sea",
	".swf":     "application/x-shockwave-flash",
	".sit":     "application/x-stuffit",
	".tcl":     "application/x-tcl",
	".tk":      "application/x-tcl",
	".der":     "application/x-x509-ca-cert",
	".pem":     "application/x-x509-ca-cert",
	".crt":     "application/x-x509-ca-cert",
	".xpi":     "application/x-xpinstall",
	".xhtml":   "application/xhtml+xml",
	".xspf":    "application/xspf+xml",
	".zip":     "application/zip",
	".bin":     "application/octet-stream",
	".exe":     "application/octet-stream",
	".dll":     "application/octet-stream",
	".deb":     "application/octet-stream",
	".dmg":     "application/octet-stream",
	".iso":     "application/octet-stream",
	".img":     "application/octet-stream",
	".msi":     "application/octet-stream",
	".msp":     "application/octet-stream",
	".msm":     "application/octet-stream",
	".mid":     "audio/midi",
	".midi":    "audio/midi",
	".kar":     "audio/midi",
	".mp3":     "audio/mpeg",
	".ogg":     "audio/ogg",
	".m4a":     "audio/x-m4a",
	".ra":      "audio/x-realaudio",
	".3gpp":    "video/3gpp",
	".3gp":     "video/3gpp",
	".ts":      "video/mp2t",
	".mp4":     "video/mp4",
	".mpeg":    "video/mpeg",
	".mpg":     "video/mpeg",
	".mov":     "video/quicktime",
	".webm":    "video/webm",
	".flv":     "video/x-flv",
	".m4v":     "video/x-m4v",
	".mng":     "video/x-mng",
	".asx":     "video/x-ms-asf",
	".asf":     "video/x-ms-asf",
	".wmv":     "video/x-ms-wmv",
	".avi":     "video/x-msvideo",
}

// HTTP Headers were copied from net/http.
const (
	// Authentication
	HeaderAuthorization      = "Authorization"
	HeaderProxyAuthenticate  = "Proxy-Authenticate"
	HeaderProxyAuthorization = "Proxy-Authorization"
	HeaderWWWAuthenticate    = "WWW-Authenticate"

	// Caching
	HeaderAge           = "Age"
	HeaderCacheControl  = "Cache-Control"
	HeaderClearSiteData = "Clear-Site-Data"
	HeaderExpires       = "Expires"
	HeaderPragma        = "Pragma"
	HeaderWarning       = "Warning"

	// Client hints
	HeaderAcceptCH         = "Accept-CH"
	HeaderAcceptCHLifetime = "Accept-CH-Lifetime"
	HeaderContentDPR       = "Content-DPR"
	HeaderDPR              = "DPR"
	HeaderEarlyData        = "Early-Data"
	HeaderSaveData         = "Save-Data"
	HeaderViewportWidth    = "Viewport-Width"
	HeaderWidth            = "Width"

	// Conditionals
	HeaderETag              = "ETag"
	HeaderIfMatch           = "If-Match"
	HeaderIfModifiedSince   = "If-Modified-Since"
	HeaderIfNoneMatch       = "If-None-Match"
	HeaderIfUnmodifiedSince = "If-Unmodified-Since"
	HeaderLastModified      = "Last-Modified"
	HeaderVary              = "Vary"

	// Connection management
	HeaderConnection = "Connection"
	HeaderKeepAlive  = "Keep-Alive"

	// Content negotiation
	HeaderAccept         = "Accept"
	HeaderAcceptCharset  = "Accept-Charset"
	HeaderAcceptEncoding = "Accept-Encoding"
	HeaderAcceptLanguage = "Accept-Language"

	// Controls
	HeaderCookie      = "Cookie"
	HeaderExpect      = "Expect"
	HeaderMaxForwards = "Max-Forwards"
	HeaderSetCookie   = "Set-Cookie"

	// CORS
	HeaderAccessControlAllowCredentials = "Access-Control-Allow-Credentials"
	HeaderAccessControlAllowHeaders     = "Access-Control-Allow-Headers"
	HeaderAccessControlAllowMethods     = "Access-Control-Allow-Methods"
	HeaderAccessControlAllowOrigin      = "Access-Control-Allow-Origin"
	HeaderAccessControlExposeHeaders    = "Access-Control-Expose-Headers"
	HeaderAccessControlMaxAge           = "Access-Control-Max-Age"
	HeaderAccessControlRequestHeaders   = "Access-Control-Request-Headers"
	HeaderAccessControlRequestMethod    = "Access-Control-Request-Method"
	HeaderOrigin                        = "Origin"
	HeaderTimingAllowOrigin             = "Timing-Allow-Origin"
	HeaderXPermittedCrossDomainPolicies = "X-Permitted-Cross-Domain-Policies"

	// Do Not Track
	HeaderDNT = "DNT"
	HeaderTk  = "Tk"

	// Downloads
	HeaderContentDisposition = "Content-Disposition"

	// Message body information
	HeaderContentEncoding = "Content-Encoding"
	HeaderContentLanguage = "Content-Language"
	HeaderContentLength   = "Content-Length"
	HeaderContentLocation = "Content-Location"
	HeaderContentType     = "Content-Type"

	// Proxies
	HeaderForwarded       = "Forwarded"
	HeaderVia             = "Via"
	HeaderXForwardedFor   = "X-Forwarded-For"
	HeaderXForwardedHost  = "X-Forwarded-Host"
	HeaderXForwardedProto = "X-Forwarded-Proto"

	// Redirects
	HeaderLocation = "Location"

	// Request context
	HeaderFrom           = "From"
	HeaderHost           = "Host"
	HeaderReferer        = "Referer"
	HeaderReferrerPolicy = "Referrer-Policy"
	HeaderUserAgent      = "User-Agent"

	// Response context
	HeaderAllow  = "Allow"
	HeaderServer = "Server"

	// Range requests
	HeaderAcceptRanges = "Accept-Ranges"
	HeaderContentRange = "Content-Range"
	HeaderIfRange      = "If-Range"
	HeaderRange        = "Range"

	// Security
	HeaderContentSecurityPolicy           = "Content-Security-Policy"
	HeaderContentSecurityPolicyReportOnly = "Content-Security-Policy-Report-Only"
	HeaderCrossOriginResourcePolicy       = "Cross-Origin-Resource-Policy"
	HeaderExpectCT                        = "Expect-CT"
	HeaderFeaturePolicy                   = "Feature-Policy"
	HeaderPublicKeyPins                   = "Public-Key-Pins"
	HeaderPublicKeyPinsReportOnly         = "Public-Key-Pins-Report-Only"
	HeaderStrictTransportSecurity         = "Strict-Transport-Security"
	HeaderUpgradeInsecureRequests         = "Upgrade-Insecure-Requests"
	HeaderXContentTypeOptions             = "X-Content-Type-Options"
	HeaderXDownloadOptions                = "X-Download-Options"
	HeaderXFrameOptions                   = "X-Frame-Options"
	HeaderXPoweredBy                      = "X-Powered-By"
	HeaderXXSSProtection                  = "X-XSS-Protection"

	// Server-sent event
	HeaderLastEventID = "Last-Event-ID"
	HeaderNEL         = "NEL"
	HeaderPingFrom    = "Ping-From"
	HeaderPingTo      = "Ping-To"
	HeaderReportTo    = "Report-To"

	// Transfer coding
	HeaderTE               = "TE"
	HeaderTrailer          = "Trailer"
	HeaderTransferEncoding = "Transfer-Encoding"

	// WebSockets
	HeaderSecWebSocketAccept     = "Sec-WebSocket-Accept"
	HeaderSecWebSocketExtensions = "Sec-WebSocket-Extensions"
	HeaderSecWebSocketKey        = "Sec-WebSocket-Key"
	HeaderSecWebSocketProtocol   = "Sec-WebSocket-Protocol"
	HeaderSecWebSocketVersion    = "Sec-WebSocket-Version"

	// Other
	HeaderAcceptPatch         = "Accept-Patch"
	HeaderAcceptPushPolicy    = "Accept-Push-Policy"
	HeaderAcceptSignature     = "Accept-Signature"
	HeaderAltSvc              = "Alt-Svc"
	HeaderDate                = "Date"
	HeaderIndex               = "Index"
	HeaderLargeAllocation     = "Large-Allocation"
	HeaderLink                = "Link"
	HeaderPushPolicy          = "Push-Policy"
	HeaderRetryAfter          = "Retry-After"
	HeaderServerTiming        = "Server-Timing"
	HeaderSignature           = "Signature"
	HeaderSignedHeaders       = "Signed-Headers"
	HeaderSourceMap           = "SourceMap"
	HeaderUpgrade             = "Upgrade"
	HeaderXDNSPrefetchControl = "X-DNS-Prefetch-Control"
	HeaderXPingback           = "X-Pingback"
	HeaderXRequestID          = "X-Request-ID"
	HeaderXRequestedWith      = "X-Requested-With"
	HeaderXRobotsTag          = "X-Robots-Tag"
	HeaderXUACompatible       = "X-UA-Compatible"
)

const (
	TextBlack = iota + 30
	TextRed
	TextGreen
	TextYellow
	TextBlue
	TextMagenta
	TextCyan
	TextWhite
)

func Black(msg string) string {
	return SetColor(msg, 0, 0, TextBlack)
}

func Red(msg string) string {
	return SetColor(msg, 0, 0, TextRed)
}

func Green(msg string) string {
	return SetColor(msg, 0, 0, TextGreen)
}

func Yellow(msg string) string {
	return SetColor(msg, 0, 0, TextYellow)
}

func Blue(msg string) string {
	return SetColor(msg, 0, 0, TextBlue)
}

func Magenta(msg string) string {
	return SetColor(msg, 0, 0, TextMagenta)
}

func Cyan(msg string) string {
	return SetColor(msg, 0, 0, TextCyan)
}

func White(msg string) string {
	return SetColor(msg, 0, 0, TextWhite)
}

func SetColor(msg string, conf, bg, text int) string {
	return fmt.Sprintf("%c[%d;%d;%dm%s%c[0m", 0x1B, conf, bg, text, msg, 0x1B)
}
