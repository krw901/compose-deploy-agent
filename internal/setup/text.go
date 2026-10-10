package setup

import (
	"cmp"
	"fmt"
	"os"
	"strings"
)

// Text holds every message the wizard shows, as fmt formats. Some formats
// use explicit argument indexes (%[2]s) where a language needs another
// word order.
type Text struct {
	// Prompter.
	ConfirmHintYes string
	ConfirmHintNo  string
	ConfirmRetry   string

	// Step headers.
	StepBind     string
	StepCertIPs  string
	StepFiles    string
	StepManifest string

	// Step 1: bind address.
	UsingIPFlag     string
	DetectIPsFailed string
	NoCandidateIP   string
	AskBindIP       string
	AskPort         string

	// Step 2: certificate IPs.
	CertIntro       string
	CertWillInclude string
	CertBindItem    string
	ConfirmIPs      string
	AskExtraIPs     string

	// Step 3: serve.env and certificates.
	EnvUnchanged           string
	EnvExists              string
	ConfirmOverwriteEnv    string
	KeepExisting           string
	BackedUp               string
	WroteEnv               string
	CACreated              string
	ServerCertUnreadable   string
	ServerCertUnchanged    string
	ServerCertCovers       string
	ConfirmReissue         string
	KeepServerCert         string
	ServerCertReissued     string
	ManifestSkippedYesMode string

	// Step 4: services.yaml.
	ManifestExists        string
	ConfirmScan           string
	ConfirmRegenerate     string
	ManifestParseFailed   string
	ListProjectsFailed    string
	NoServiceSelected     string
	StatusReady           string
	StatusRewrite         string
	StatusManaged         string
	NoManageableService   string
	AskServices           string
	DroppedHeader         string
	ConfirmRemoveDropped  string
	UnmatchedHeader       string
	UnmatchedItem         string
	ConfirmKeepUnmatched  string
	NotSelectable         string
	ManualRewriteHint     string
	ConfirmApply          string
	NothingChanged        string
	AskCurrentTag         string
	PreviewHeader         string
	PreviewComposeFile    string
	PreviewLine           string
	Wrote                 string
	ReasonNoImage         string
	ReasonDigest          string
	ReasonRepoVariable    string
	ReasonTagExpression   string
	ReasonMultipleFiles   string
	ReasonConfigUnread    string
	ReasonFileUnread      string
	ReasonTagCollision    string
	ReasonParseCompose    string
	ReasonEmptyCompose    string
	ReasonNotInline       string
	ReasonFlowStyle       string
	ReasonImageNotInline  string
	ReasonImageAlias      string
	ReasonImageAnchor     string
	ReasonImageBlock      string
	ReasonImageNotInPlace string

	// Answer validation.
	ErrIPRequired    string
	ErrNotInList     string
	ErrUnspecifiedIP string
	ErrBadPort       string
	ErrZeroPort      string
	ErrBadIP         string
	ErrBadName       string
	ErrNotIPv4       string
	ErrBadRange      string
	ErrNotNumber     string
	ErrOutOfRange    string
	ErrBadTag        string

	// Step 5 and the no-terminal hint, printed by cmd/deploy-agent.
	NonInteractiveHint string
	FinishChecking     string
	ConfigTestOK       string
	ManifestMissing    string
	CustomPaths        string
	NextSteps          string
	StartHint          string
	UnitUnmanageable   string
	ConfirmEnable      string
	ConfirmRestart     string
	UnitRestarted      string
	UnitEnabled        string
}

var english = Text{
	ConfirmHintYes: "Y/n",
	ConfirmHintNo:  "y/N",
	ConfirmRetry:   "  please answer y or n\n",

	StepBind:     "\nStep 1/4: bind address\n",
	StepCertIPs:  "\nStep 2/4: server certificate names\n",
	StepFiles:    "\nStep 3/4: serve.env and certificates\n",
	StepManifest: "\nStep 4/4: services.yaml\n",

	UsingIPFlag:     "  using --ip %s\n",
	DetectIPsFailed: "  cannot detect local IPs: %v\n",
	NoCandidateIP:   "  no usable IPv4 address detected; enter the internal IP manually\n",
	AskBindIP:       "Internal IP to bind (number or IP address)",
	AskPort:         "Port",

	CertIntro: "  The certificate always covers the bind IP %s. Add every other address clients\n" +
		"  use to reach the agent: another IP, or a DNS name such as agent.example.com.\n",
	CertWillInclude: "  The server certificate will include:\n",
	CertBindItem:    "    - %s (bind)\n",
	ConfirmIPs:      "Use these addresses?",
	AskExtraIPs:     "Additional IPs or DNS names, comma-separated (Enter to skip)",

	EnvUnchanged:           "  %s already has %s=%s, unchanged\n",
	EnvExists:              "  %s already exists with %s=%q\n",
	ConfirmOverwriteEnv:    "Overwrite it with %s=%s?",
	KeepExisting:           "  keeping the existing %s\n",
	BackedUp:               "  backed up to %s\n",
	WroteEnv:               "  wrote %s (%s=%s)\n",
	CACreated:              "  created the local CA in %s and a server certificate for %s\n",
	ServerCertUnreadable:   "  the local CA exists and is kept, but the server certificate cannot be read: %v\n",
	ServerCertUnchanged:    "  the local CA exists and the server certificate already covers %s, unchanged\n",
	ServerCertCovers:       "  the local CA exists and is kept; the server certificate covers %s\n",
	ConfirmReissue:         "Reissue the server certificate for %s? (client certificates stay valid)",
	KeepServerCert:         "  keeping the current server certificate; `serve -t` fails if it does not cover the bind IP\n",
	ServerCertReissued:     "  server certificate reissued for %s\n",
	ManifestSkippedYesMode: "  skipped in --yes mode; write %s by hand (see README) or rerun `deploy-agent setup` interactively\n",

	ManifestExists:        "  %s already exists\n",
	ConfirmScan:           "Scan docker compose projects to generate it?",
	ConfirmRegenerate:     "Regenerate it from docker compose projects? (the current file is backed up)",
	ManifestParseFailed:   "  cannot parse the existing %s (%v); its services are not carried over\n",
	ListProjectsFailed:    "  cannot list docker compose projects: %v\n  skipping; write %s by hand (see README)\n",
	NoServiceSelected:     "  no service selected, %s not written\n",
	StatusReady:           "ready",
	StatusRewrite:         "compose file will be changed to use %s",
	StatusManaged:         "managed as %s; %s",
	NoManageableService:   "  no compose service can be managed by the agent\n",
	AskServices:           "Services to manage (e.g. 1,3 or 2-4, all or none)",
	DroppedHeader:         "  These services are managed by the current services.yaml but not selected:\n",
	ConfirmRemoveDropped:  "Remove them from services.yaml? Pipelines can no longer deploy them",
	UnmatchedHeader:       "  These services are managed by the current services.yaml but were not found above,\n  or cannot be changed automatically:\n",
	UnmatchedItem:         "    - %s (service %s in %s)\n",
	ConfirmKeepUnmatched:  "Keep them unchanged in services.yaml?",
	NotSelectable:         "  Not selectable:\n",
	ManualRewriteHint:     "        to manage it, set its image directly on the service to <repo>:${%[1]s:?%[1]s is required}\n",
	ConfirmApply:          "Apply these changes?",
	NothingChanged:        "  nothing was changed\n",
	AskCurrentTag:         "Tag %s currently runs (%s is not set in %s)",
	PreviewHeader:         "\n  The following changes will be made:\n",
	PreviewComposeFile:    "  %s (backed up first)\n",
	PreviewLine:           "    line %d:\n      - %s\n      + %s\n",
	Wrote:                 "  wrote %s\n",
	ReasonNoImage:         "service has no image (build-only services cannot be deployed by the agent)",
	ReasonDigest:          "digest references (repo@sha256:...) cannot be retagged",
	ReasonRepoVariable:    "image %q uses variables outside the tag; the repository must be literal",
	ReasonTagExpression:   "tag %q is not a single ${VAR} reference",
	ReasonMultipleFiles:   "project uses %d compose files (%s); only single-file projects are supported",
	ReasonConfigUnread:    "cannot read compose config: %v",
	ReasonFileUnread:      "cannot read compose file: %v",
	ReasonTagCollision:    "tag variable %s collides with service %s; rename one of them",
	ReasonParseCompose:    "parse compose file: %w",
	ReasonEmptyCompose:    "compose file is empty",
	ReasonNotInline:       "service is not defined directly in the compose file (include or YAML aliases are not supported)",
	ReasonFlowStyle:       "flow-style service definitions ({...}) cannot be rewritten",
	ReasonImageNotInline:  "image is not set directly on the service (extends or YAML merge keys are not supported)",
	ReasonImageAlias:      "image value is a YAML alias",
	ReasonImageAnchor:     "image value is a YAML anchor that other services may share",
	ReasonImageBlock:      "image value is a multi-line block scalar",
	ReasonImageNotInPlace: "line %d: image value contains escapes or spans lines; edit it manually",

	ErrIPRequired:    "an IP address is required (pass --ip with --yes)",
	ErrNotInList:     "%d is not in the list",
	ErrUnspecifiedIP: "never bind 0.0.0.0; choose an internal IP",
	ErrBadPort:       "%q is not a valid port",
	ErrZeroPort:      "port must not be 0",
	ErrBadIP:         "invalid IP address %q",
	ErrBadName:       "%q is not a valid IP address or DNS name",
	ErrNotIPv4:       "%q is not an IPv4 address",
	ErrBadRange:      "invalid range %q",
	ErrNotNumber:     "%q is not a number",
	ErrOutOfRange:    "%d is out of range 1-%d",
	ErrBadTag:        "%q is not a valid image tag",

	NonInteractiveHint: `deploy-agent setup: stdin is not a terminal, skipping the interactive setup.
To configure the agent, rerun it from a terminal (e.g. ssh -t <host> sudo deploy-agent setup),
or non-interactively:
  sudo deploy-agent setup --yes --ip <internal-ip> [--extra-ips <ip|dns>,...]
If the agent is already configured, restart it to pick up a new binary:
  systemctl restart deploy-agent
`,
	FinishChecking: "\nChecking the configuration (serve -t)\n",
	ConfigTestOK:   "deploy-agent: configuration test is successful (%d services, server certificate valid until %s)\n",
	ManifestMissing: "  %s does not exist yet; write it (see deploy/services.example.yaml), then run:\n" +
		"    deploy-agent serve -t --bind %s\n    systemctl daemon-reload && systemctl enable --now %s\n",
	CustomPaths: "  custom --env-file/--manifest/--data-dir in use; the %s systemd unit reads the default paths,\n" +
		"  so it is not started or restarted here\n",
	NextSteps: "\nNext, issue a client certificate for each pipeline and store it in its credentials:\n" +
		"  deploy-agent ca issue -n <pipeline-name>\n",
	StartHint: "  start the agent with: systemctl daemon-reload && systemctl enable --now %[1]s\n" +
		"  (or systemctl restart %[1]s if it is already running)\n",
	UnitUnmanageable: "  cannot manage the systemd unit: %v\n",
	ConfirmEnable:    "Enable and start %s now?",
	ConfirmRestart:   "%s is running; restart it to apply the configuration?",
	UnitRestarted:    "  %s restarted\n",
	UnitEnabled:      "  %[1]s enabled and started; check it with: systemctl status %[1]s\n",
}

var chinese = Text{
	ConfirmHintYes: "Y/n, 默认 y",
	ConfirmHintNo:  "y/N, 默认 n",
	ConfirmRetry:   "  请回答 y 或 n（也可以输入 是 或 否）\n",

	StepBind:     "\n第 1/4 步：绑定地址\n",
	StepCertIPs:  "\n第 2/4 步：服务端证书地址\n",
	StepFiles:    "\n第 3/4 步：serve.env 与证书\n",
	StepManifest: "\n第 4/4 步：services.yaml\n",

	UsingIPFlag:     "  使用 --ip %s\n",
	DetectIPsFailed: "  无法检测本机 IP：%v\n",
	NoCandidateIP:   "  未检测到可用的 IPv4 地址，请手动输入内网 IP\n",
	AskBindIP:       "要绑定的内网 IP（输入编号或 IP 地址）",
	AskPort:         "端口",

	CertIntro: "  证书始终包含绑定 IP %s。请补充客户端访问 agent 时还会用到的其他 IP 或域名，\n" +
		"  例如前置四层代理的 IP，或 agent.example.com 这样的域名。\n",
	CertWillInclude: "  服务端证书将包含：\n",
	CertBindItem:    "    - %s（绑定 IP）\n",
	ConfirmIPs:      "使用这些地址？",
	AskExtraIPs:     "其他 IP 或域名，多个用逗号分隔（不需要直接回车）",

	EnvUnchanged:           "  %s 中已是 %s=%s，保持不变\n",
	EnvExists:              "  %s 已存在，当前 %s=%q\n",
	ConfirmOverwriteEnv:    "覆盖为 %s=%s？",
	KeepExisting:           "  保留现有的 %s\n",
	BackedUp:               "  已备份到 %s\n",
	WroteEnv:               "  已写入 %s（%s=%s）\n",
	CACreated:              "  已在 %s 创建本机 CA，并为 %s 签发服务端证书\n",
	ServerCertUnreadable:   "  本机 CA 已存在，保持不变；但无法读取服务端证书：%v\n",
	ServerCertUnchanged:    "  本机 CA 已存在，服务端证书已包含 %s，保持不变\n",
	ServerCertCovers:       "  本机 CA 已存在，保持不变；当前服务端证书包含 %s\n",
	ConfirmReissue:         "为 %s 重签服务端证书？（已签发的客户端证书仍然有效）",
	KeepServerCert:         "  保留当前服务端证书；如果它不包含绑定 IP，`serve -t` 会失败\n",
	ServerCertReissued:     "  已为 %s 重签服务端证书\n",
	ManifestSkippedYesMode: "  --yes 模式下跳过；请手动编写 %s（见 README），或以交互方式重新运行 `deploy-agent setup`\n",

	ManifestExists:        "  %s 已存在\n",
	ConfirmScan:           "扫描 docker compose 项目来生成它？",
	ConfirmRegenerate:     "根据 docker compose 项目重新生成？（会先备份现有文件）",
	ManifestParseFailed:   "  无法解析现有的 %s（%v），其中的服务不会被沿用\n",
	ListProjectsFailed:    "  无法列出 docker compose 项目：%v\n  已跳过；请手动编写 %s（见 README）\n",
	NoServiceSelected:     "  未选择任何服务，未写入 %s\n",
	StatusReady:           "无需改动",
	StatusRewrite:         "将改写 compose 文件，改用 %s",
	StatusManaged:         "已纳管为 %s；%s",
	NoManageableService:   "  没有可由 agent 管理的 compose 服务\n",
	AskServices:           "要管理的服务（如 1,3 或 2-4，也可输入 all 或 none）",
	DroppedHeader:         "  以下服务由当前 services.yaml 管理，但本次未选中：\n",
	ConfirmRemoveDropped:  "从 services.yaml 中移除它们？移除后流水线将无法再部署它们",
	UnmatchedHeader:       "  以下服务由当前 services.yaml 管理，但未在上面列出，或无法自动改写：\n",
	UnmatchedItem:         "    - %[1]s（%[3]s 中的服务 %[2]s）\n",
	ConfirmKeepUnmatched:  "在 services.yaml 中原样保留它们？",
	NotSelectable:         "  不可选择：\n",
	ManualRewriteHint:     "        如需管理，请在该服务上直接把 image 改为 <repo>:${%[1]s:?%[1]s is required}\n",
	ConfirmApply:          "应用以上改动？",
	NothingChanged:        "  未做任何改动\n",
	AskCurrentTag:         "%s 当前运行的 tag（%s 未在 %s 中设置）",
	PreviewHeader:         "\n  将进行以下改动：\n",
	PreviewComposeFile:    "  %s（修改前先备份）\n",
	PreviewLine:           "    第 %d 行：\n      - %s\n      + %s\n",
	Wrote:                 "  已写入 %s\n",
	ReasonNoImage:         "服务没有 image（agent 无法部署只有 build 的服务）",
	ReasonDigest:          "digest 引用（repo@sha256:...）无法改用 tag 变量",
	ReasonRepoVariable:    "image %q 在 tag 之外使用了变量，仓库地址必须是固定值",
	ReasonTagExpression:   "tag %q 不是单个 ${VAR} 引用",
	ReasonMultipleFiles:   "项目使用了 %d 个 compose 文件（%s），只支持单文件项目",
	ReasonConfigUnread:    "无法读取 compose 配置：%v",
	ReasonFileUnread:      "无法读取 compose 文件：%v",
	ReasonTagCollision:    "tag 变量 %s 与服务 %s 冲突，请重命名其中一个",
	ReasonParseCompose:    "解析 compose 文件失败：%w",
	ReasonEmptyCompose:    "compose 文件为空",
	ReasonNotInline:       "服务不是直接定义在该 compose 文件中（不支持 include 或 YAML 别名）",
	ReasonFlowStyle:       "无法改写 flow 风格（{...}）的服务定义",
	ReasonImageNotInline:  "image 没有直接写在该服务上（不支持 extends 或 YAML 合并键）",
	ReasonImageAlias:      "image 的值是 YAML 别名",
	ReasonImageAnchor:     "image 的值是 YAML 锚点，可能被其他服务共用",
	ReasonImageBlock:      "image 的值是多行块标量",
	ReasonImageNotInPlace: "第 %d 行：image 的值包含转义或跨行，请手动修改",

	ErrIPRequired:    "必须提供 IP 地址（--yes 模式下请传 --ip）",
	ErrNotInList:     "列表中没有编号 %d",
	ErrUnspecifiedIP: "不能绑定 0.0.0.0，请选择内网 IP",
	ErrBadPort:       "%q 不是有效的端口",
	ErrZeroPort:      "端口不能为 0",
	ErrBadIP:         "%q 不是有效的 IP 地址",
	ErrBadName:       "%q 不是有效的 IP 地址或域名",
	ErrNotIPv4:       "%q 不是 IPv4 地址",
	ErrBadRange:      "%q 不是有效的范围",
	ErrNotNumber:     "%q 不是数字",
	ErrOutOfRange:    "%d 超出范围 1-%d",
	ErrBadTag:        "%q 不是有效的镜像 tag",

	NonInteractiveHint: `deploy-agent setup：标准输入不是终端，跳过交互式安装向导。
如需配置 agent，请在终端中重新运行（例如 ssh -t <host> sudo deploy-agent setup），
或以非交互方式运行：
  sudo deploy-agent setup --yes --ip <内网 IP> [--extra-ips <ip|域名>,...]
如果 agent 已经配置好，重启服务即可使用新的二进制：
  systemctl restart deploy-agent
`,
	FinishChecking: "\n检查配置（serve -t）\n",
	ConfigTestOK:   "deploy-agent：配置检查通过（%d 个服务，服务端证书有效期至 %s）\n",
	ManifestMissing: "  %s 还不存在；请先编写（参考 deploy/services.example.yaml），然后执行：\n" +
		"    deploy-agent serve -t --bind %s\n    systemctl daemon-reload && systemctl enable --now %s\n",
	CustomPaths: "  使用了自定义的 --env-file/--manifest/--data-dir；%s 的 systemd unit 只读取默认路径，\n" +
		"  因此这里不启动也不重启服务\n",
	NextSteps: "\n下一步：为每条流水线签发客户端证书，并存入流水线凭据：\n" +
		"  deploy-agent ca issue -n <流水线名>\n",
	StartHint: "  启动 agent：systemctl daemon-reload && systemctl enable --now %[1]s\n" +
		"  （如果已在运行，执行 systemctl restart %[1]s）\n",
	UnitUnmanageable: "  无法管理 systemd unit：%v\n",
	ConfirmEnable:    "现在启用并启动 %s？",
	ConfirmRestart:   "%s 正在运行，重启以应用新配置？",
	UnitRestarted:    "  已重启 %s\n",
	UnitEnabled:      "  已启用并启动 %[1]s；可用 systemctl status %[1]s 查看状态\n",
}

// DetectText picks Chinese when the locale's character set is UTF-8, so
// the terminal can display it, and English otherwise. The locale is the
// first non-empty of LC_ALL, LC_CTYPE and LANG, as POSIX specifies.
func DetectText() Text {
	if localeIsUTF8(cmp.Or(os.Getenv("LC_ALL"), os.Getenv("LC_CTYPE"), os.Getenv("LANG"))) {
		return chinese
	}
	return english
}

// localeIsUTF8 reports whether a locale name such as zh_CN.UTF-8 or
// C.utf8@euro names the UTF-8 character set.
func localeIsUTF8(locale string) bool {
	_, rest, ok := strings.Cut(locale, ".")
	if !ok {
		return false
	}
	charset, _, _ := strings.Cut(rest, "@")
	switch strings.ToLower(charset) {
	case "utf-8", "utf8":
		return true
	default:
		return false
	}
}

// TextByName returns the text for a --lang value: "zh", "en" or "auto".
func TextByName(name string) (Text, error) {
	switch name {
	case "zh":
		return chinese, nil
	case "en":
		return english, nil
	case "auto":
		return DetectText(), nil
	default:
		return Text{}, fmt.Errorf("unknown language %q, want zh, en or auto", name)
	}
}
