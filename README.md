# deploy-agent

Compose 服务镜像自动更新 agent：CI 流水线镜像推送完成后，自动触发目标 VPS 上的 `docker compose` 服务更新，失败自动回滚。MIT 协议发布，见 [LICENSE](./LICENSE)。

`deploy-agent` 是单一 Go 静态二进制，通过子命令区分角色：

- **Agent（`serve` / `ca`）**：部署在目标 VPS 上的常驻服务，由 systemd 托管。
- **Client（`deploy`）**：部署在 CI 流水线脚本任务中，用来提交部署请求。

两者通过 mTLS 通信，身份由本机自签的证书体系承载，不使用 token。

## 目录结构

```text
cmd/deploy-agent/        # 二进制入口（serve / setup / ca / deploy / update / self-check / self-upgrade / version）
internal/                # 各功能模块
deploy/                  # systemd unit、清单示例、安装脚本
scripts/                 # CI 脚本模板、docker 集成测试
```

## 构建

```bash
make build            # 产出 bin/deploy-agent（当前平台）
make test             # 运行单测
```

也可以手动交叉编译：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags "-X main.version=1.0.0" \
  -o deploy-agent ./cmd/deploy-agent
```

---

## 一、Agent 使用说明（部署在目标 VPS 上）

### 1. 安装

`deploy/install.sh` 是自包含脚本（不依赖同目录下的其他文件），**不需要把整个仓库 clone 到 VPS**，只要把这一个文件传上去即可，例如：

```bash
scp deploy/install.sh vps:/tmp/install.sh
ssh -t vps bash /tmp/install.sh {version}   # {version} 换成具体版本号，或用 latest
```

注意 `ssh` 要带 `-t` 分配终端，否则无法进入下面的交互式向导（见「无人值守安装」）。也可以直接在 VPS 上执行（前提是仓库已经在本机）：

```bash
bash deploy/install.sh {version}
```

该脚本会：

1. 从 `https://github.com/krwu/compose-deploy-agent/releases/download/{version}/deploy-agent` 下载二进制（`latest` 用 `releases/latest/download/deploy-agent`），并用同目录的 `deploy-agent.sha256` 校验 sha256
2. 安装到 `/usr/local/bin/deploy-agent`
3. 创建 `/etc/deploy-agent/`、`/var/lib/deploy-agent/`（权限 0700）
4. 首次安装时写入 `/etc/systemd/system/deploy-agent.service`（unit 内容内嵌在脚本里，与仓库中的 `deploy/deploy-agent.service` 保持一致，无需额外拷贝）
5. 进入安装向导 `deploy-agent setup`（见下文）。安装的是不带 `setup` 子命令的旧版本时，只打印后续手动步骤

#### 安装向导（`deploy-agent setup`）

向导依次完成首次安装需要的全部配置，每一步都会先展示结果再确认：

1. **绑定地址**：自动检测本机 IPv4 地址（排除 loopback、link-local 以及 `docker0`、`br-*`、`veth*` 等 docker 网卡）并列出供选择，也可以直接输入 IP；端口默认 `8866`
2. **证书地址**：补充客户端还会经由哪些地址访问 agent，可以是其他 IP（如前置的四层代理）或域名（如 `agent.example.com`），多个用逗号分隔，不需要时直接回车；服务端证书已存在时，默认沿用证书中已有的其他 IP 和域名（回车保留，输入 `none` 清空）。然后列出最终写入服务端证书 SAN 的全部地址供确认。监听仍然绑在第 1 步的 IP 上；域名要能访问，需要解析到这台机器，并且客户端 `--endpoint` 使用该域名
3. **serve.env 与证书**：写入 `/etc/deploy-agent/serve.env`；本机 CA 不存在时生成 CA 和服务端证书。**CA 已存在时绝不覆盖**，只在服务端证书的 IP 或域名与本次确认的不一致时询问是否重签服务端证书（已签发的客户端证书不受影响）
4. **services.yaml**（可选）：扫描本机的 docker compose 项目，按编号选择要纳管的服务后自动生成清单（已有清单时，已纳管的服务默认选中），细节见下文「清单配置」。选择时可输入 `1,3`、`2-4`、`all` 或 `none`，直接回车采用方括号中的默认值
5. **检查与启动**：执行与 `serve -t` 相同的配置检查，通过后询问是否 `systemctl enable --now deploy-agent`（已在运行则询问是否重启）

已存在的 `serve.env`、`services.yaml` 默认保留（`services.yaml` 已存在时「是否重新生成」默认为否）；选择覆盖时会先备份为 `.bak`。向导可以重复执行（`sudo deploy-agent setup`），例如新增代理 IP 或纳管新服务时，已有的配置会作为默认值。

用 `curl ... | bash` 方式执行 `install.sh` 时，向导会从当前终端（`/dev/tty`）读取回答。

向导的语言按终端的区域设置自动选择：`LC_ALL`、`LC_CTYPE`、`LANG` 中第一个非空值的字符集为 UTF-8（如 `zh_CN.UTF-8`、`en_US.UTF-8`、`C.UTF-8`）时用中文，否则（如 `C`、`POSIX`、`zh_CN.GBK`）用英文。可以用 `--lang zh` 或 `--lang en` 指定。确认题的提示为 `[Y/n]` / `[y/N]`（中文界面写作 `[Y/n, 默认 y]` / `[y/N, 默认 n]`），回答 `y`/`n`，中文界面也可以回答 `是`/`否`。向导里的配置检查结果跟向导语言走，证书有效期显示为 `2006-01-02 15:04:05`；单独执行的 `serve -t` 仍打印英文，写入文件中的注释也始终是英文。

#### 无人值守安装

没有终端（`ssh` 不带 `-t`）时向导只打印后续步骤。需要脚本化安装时，传 `--yes` 并通过参数给出答案，`install.sh` 会把版本号之后的参数透传给 `setup`：

```bash
ssh vps bash /tmp/install.sh {version} --yes --ip 10.0.0.5 --extra-ips 10.0.0.6
```

| 参数 | 说明 |
| --- | --- |
| `--ip` | 绑定的内网 IP；`--yes` 模式下必填（`serve.env` 中已有时可省略） |
| `--port` | 绑定端口，默认 `8866` |
| `--extra-ips` | 服务端证书额外包含的 IP 或域名，逗号分隔；不传时沿用现有证书中的其他 IP 和域名 |
| `--yes` | 全部采用默认值：不覆盖已有文件、不生成 `services.yaml`、不启动服务 |
| `--env-file` / `--manifest` / `--data-dir` | 使用非默认路径时指定。此时向导只做配置检查、不启动或重启服务，因为 systemd unit 只读取默认路径 |
| `--lang` | 向导语言：`zh`、`en` 或 `auto`（默认，按区域设置选择，见上文） |

#### 手动配置（备选）

也可以跳过向导，手动完成配置：

- 复制 `deploy/serve.env.example` 为 `/etc/deploy-agent/serve.env`，填入本机内网 IP（`DEPLOY_AGENT_BIND=10.0.0.5:8866`）
- 参考 `deploy/services.example.yaml` 编写 `/etc/deploy-agent/services.yaml`（见下文「清单配置」）
- 按下一节执行 `deploy-agent ca init`

### 2. 证书管理

Agent 使用每机自签的本地 CA，只信任自己签发的客户端证书。安装向导已经完成 CA 和服务端证书的初始化；手动配置时用 `ca init` 完成。以下命令均需在目标机上执行：

```bash
# 生成本机 CA + 服务端证书（仅手动配置时需要；--ip 为本机内网 IP，会写入服务端证书的 SAN）
deploy-agent ca init --ip 10.0.0.5

# 客户端还会用其他 IP 或域名访问时，把它们和绑定 IP 写在同一次 --ip 里。
# 证书同时带 IP SAN 和 DNS SAN，https://10.0.0.5:8866 和 https://agent.example.com:8866 都能通过校验
deploy-agent ca init --ip 10.0.0.5,10.0.0.6,agent.example.com

# 为一条流水线签发客户端证书，--name（或简写 -n）用于标识用途，建议用流水线名
deploy-agent ca issue -n ci-web
```

`ca issue` 会把 PEM 证书包（客户端证书 + 私钥 + 服务端 CA）打印到 stdout，**只显示一次，agent 不会持久化这份内容**。把它完整复制，作为多行文本存入 CI 密钥，由流水线注入环境变量 `DEPLOY_AGENT_CREDENTIAL`。

其他证书管理命令：

```bash
deploy-agent ca list                        # 查看所有已签发证书（指纹、名称、有效期、是否吊销）
deploy-agent ca revoke -f <fingerprint>      # 吊销单个证书（-f/--fingerprint），立即生效，不影响其他证书
deploy-agent ca reissue-server                 # 续期：沿用当前证书里的 IP 和域名
deploy-agent ca reissue-server --ip 10.0.0.5,agent.example.com
```

`ca reissue-server` 省略 `--ip` 时，新证书沿用当前服务端证书上的 IP 和域名，只更换证书本身（用于到期续期）。传入 `--ip` 时，新证书只包含这一次列出的名字，因此改地址时要列全（如 `--ip 10.0.0.5,10.0.0.6,agent.example.com`），至少要有一个 IP。重签只替换服务端证书，不动 CA，已签发的客户端证书无需更新。

所有 `ca` 子命令都必须在目标机上通过 SSH 手动执行，agent 的 HTTP 接口不提供签发能力。

默认数据目录是 `/var/lib/deploy-agent`，如需自定义，所有 `ca` 子命令都支持 `--data-dir` 参数。

### 3. 清单配置（`services.yaml`）

每个纳管的服务只需 4 个必填字段：

```yaml
version: 1
services:
  web:                           # 逻辑服务名，API 中的 service 字段
    dir: /srv/web                # compose 项目所在的绝对路径
    file: compose.yml            # compose 文件名（相对 dir）
    service: web                 # compose 文件里的服务名
    image: registry.example.com/web   # 镜像仓库地址（不含 tag）
    # tag_env: IMAGE_TAG         # 可选，默认 IMAGE_TAG
```

对应的 compose 文件需要用该环境变量引用镜像 tag，并要求变量必须存在：

```yaml
services:
  web:
    image: registry.example.com/web:${IMAGE_TAG:?IMAGE_TAG is required}
```

agent 拉取时使用 `docker compose pull --policy always`，因此即使始终推送同一个 tag 也能更新；这要求 Docker Compose 支持 `pull --policy`（可用 `docker compose pull --help | grep policy` 确认）。服务的 `pull_policy` 不能设为 `never` 或 `build`，否则预检返回 `config_invalid`；不设置、`missing`、`always` 均可。

不在清单中的服务一律拒绝部署（清单即白名单）。修改清单后需要 `systemctl restart deploy-agent` 生效。

#### 由安装向导生成清单

向导通过 `docker compose ls -a` 找到本机的 compose 项目（包括已停止的），列出每个服务及其镜像。生成的清单中逻辑服务名默认为 compose 服务名；多个项目有同名服务、或该名字已被现有清单中的其他服务占用时，加上 `<项目名>-` 前缀（已纳管的服务保留原名，见下文）。

向导生成的服务统一使用 **`{SERVICE}_TAG`** 作为 `tag_env`：`{SERVICE}` 为 compose 服务名转大写，非字母数字字符替换为 `_`（如 `api-web` 对应 `API_WEB_TAG`），同一目录下不会重复。compose 文件中的 image 还没有这样引用时，向导会在确认后改写 compose 文件：

- tag 写死（`repo:1.2.3`，或不写 tag）、或引用了其他变量（如 `${IMAGE_TAG:?}`、`${TAG:-latest}`）的 image 行，改写为 `repo:${API_WEB_TAG:?API_WEB_TAG is required}`。只替换这一行的值，注释和格式保持不变，原文件先备份为 `.bak`
- 在项目目录的 `.env` 中写入当前正在使用的 tag（如 `API_WEB_TAG=1.2.3`），改写后手动 `docker compose up` 的结果不变；原来的变量行保留不删。当前 tag 无法从 compose 文件和 `.env` 中确定时，向导会要求手动输入
- 写入后用 `docker compose config` 校验每个服务解析出的镜像与改写前一致，否则还原所有改动并报错

以下情况无法自动处理，会列出原因但不可选择，需要手动把 image 改成上面的形式：digest 引用（`repo@sha256:...`）、镜像仓库地址中含变量、image 来自 `extends` / `include` / YAML 锚点或合并键、项目使用多个 compose 文件、build-only 服务。

已有 `services.yaml` 时选择重新生成，向导会读取现有清单：

- 已纳管的服务在列表中标注 `managed as <逻辑名>` 并默认选中，**保留原有的逻辑名**（流水线按逻辑名部署），但同样统一改写为 `{SERVICE}_TAG`，避免旧变量名与其他服务自动生成的变量名冲突
- 取消选中已纳管的服务时会列出它们并确认，默认不移除
- 清单中有、但扫描不到或无法自动改写的服务，确认后原样保留（默认保留）
- 改写了已纳管服务的 `tag_env` 后，需要重启 deploy-agent 才能使用新清单，向导最后一步会询问是否重启

手写清单时 `tag_env` 的默认值仍是 `IMAGE_TAG`，不受影响。

向导重新生成清单时，下文的 `image_ref`、`post_deploy`、`access` 只对「原样保留」的服务和段落有效：被重新选中的已纳管服务会按扫描结果重建，`access` 段不会写回。使用这些字段的清单请手工维护，重新生成后务必核对。

#### 只接受 digest（`image_ref: digest`，可选）

tag 可以被重新指向别的镜像，有推送权限的人就能改变「同一个 tag」实际部署的内容。对安全要求高的服务可以改为只接受镜像 digest：

```yaml
services:
  web:
    dir: /srv/web
    file: compose.yml
    service: web
    image: ghcr.io/example/web
    image_ref: digest            # 默认 tag
    tag_env: IMAGE_DIGEST        # 可选，便于区分；默认仍为 IMAGE_TAG
```

compose 文件改为用 `@` 引用：

```yaml
services:
  web:
    image: ghcr.io/example/web@${IMAGE_DIGEST:?IMAGE_DIGEST is required}
```

- 请求必须带 `digest`（客户端 `--digest sha256:<64 位小写十六进制>`），带 `tag` 一律返回 400；格式严格校验为 `^sha256:[a-f0-9]{64}$`，写进 `.env` 的就是这个值，不可能注入换行或其他引用
- 反过来，未开启 `image_ref: digest` 的服务不接受 `digest`，行为与之前完全一致
- 幂等判断、回滚与 `last_healthy` 都以 digest 为准，回滚时 `.env` 恢复为上一个 digest。从 tag 模式切换过来后，旧的 tag 不能作为回滚目标（会报告 `no usable rollback target`），切换后第一次成功部署起即可正常回滚
- 部署记录与状态接口中的 `target_tag`、`running_tag`、`rolled_back_to` 等字段此时填的是 digest

#### 部署后执行固定命令（`post_deploy`，可选）

新版本通过就绪检查后，可以在容器里执行一条固定命令（例如数据库迁移、初始化）：

```yaml
services:
  web:
    # ...
    post_deploy:
      user: www-data                         # 必填，不允许 root
      args: [php, artisan, "lsky:preview-init"]
      # service: web                         # 可选，在哪个 compose 服务里执行，默认就是本服务
      # timeout: 3m                          # 可选，默认 3m，上限 5m
```

agent 实际执行的是 `docker compose -f <file> exec -T -u <user> <service> <args...>`，以参数数组调用，不经过 shell。

- 命令、用户、服务全部只来自本机清单，部署请求无法影响：请求体只允许 `service` 和 `tag`/`digest` 两个字段，带任何其他字段都返回 400
- `user` 必填，并且拒绝 root：`root`、`0`（含 `00` 等）以及 `root:…`、`0:…`、`…:root`、`…:0` 都会让清单加载失败。不提供默认值，因为镜像的默认用户往往就是 root
- 命令退出码非 0 或超时，都算部署失败：结果为 `post_deploy_failed`，并像就绪失败一样**自动回滚**到上一个版本，`message` 里写明退出码或超时（以及 stderr 末尾一段）。回滚后不会再次执行该命令；首次部署没有可回滚的版本时只报告失败
- 超时会 SIGKILL 整个 `docker compose exec` 进程组。注意 `docker compose exec` 客户端被杀后，容器里的进程不一定随之结束；回滚会用旧镜像重建容器，从而结束它。首次部署（无回滚目标）超时时，请自行确认容器内是否有残留进程
- 结果为 `noop`（目标版本已在运行）时不执行
- 整次部署仍受 10 分钟总时限约束，`timeout` 上限 5 分钟是为了给回滚留出时间

#### 客户端证书与服务绑定（`access`，可选）

默认情况下，任何已登记且未吊销的客户端证书都可以部署清单里的任何服务。需要按证书限制时，在清单顶层加 `access`：

```yaml
access:
  require_binding: true          # 默认 false，见下文
  clients:
    ci-main:                     # 证书名称（ca issue -n），或 ca list 中的指纹
      services: [web]
    ci-preview:
      services: [web-preview]
```

- 列在 `clients` 里的证书只能操作自己的 `services`；对其他服务的 `POST /v1/deploy`、`GET /v1/status/{service}`、`GET /v1/deploys/{id}` 返回 403 `cert_not_allowed`，并记录告警日志（证书名、指纹、服务）
- 同一张证书既可按名称也可按指纹配置，指纹优先。按名称配置时，同名的所有证书共享该绑定（重签同名证书无需改清单），吊销仍按单张证书生效
- `require_binding: false`（默认，兼容旧行为）：没有列出的证书仍可操作所有服务；`require_binding: true`：没有列出的证书一律 403。建议所有证书都配置绑定后打开它
- `clients` 里引用了清单中不存在的服务、或 `services` 为空，清单加载失败
- 没有配置 `access`、或 `require_binding` 为 false 时，`serve` 启动时和 `serve -t` 都会打印告警，提示存在可以部署所有服务的证书
- 本机 unix socket（`deploy-agent update`）不受绑定限制，其信任根是主机 root 权限

### 4. 启动

安装向导的最后一步会询问是否启动；手动配置时执行：

```bash
systemctl daemon-reload
systemctl enable --now deploy-agent
```

验证存活（注意端口是 `8867`、明文 HTTP，不是主端口 `8866`）：

```bash
curl http://127.0.0.1:8867/healthz
```

### 5. Agent 对外提供的接口

监听在内网地址的 `8866` 端口，除 `/v1/version` 外全部走 mTLS：

| 方法与路径 | 说明 |
| --- | --- |
| `POST /v1/deploy` | 提交部署请求，body 为 `{"service": "...", "tag": "..."}` 或 `{"service": "...", "digest": "sha256:..."}`，返回 202 和 `deploy_id`。未知字段、同时带 tag 和 digest 返回 400；body 超过 64 KiB 返回 413 |
| `GET /v1/deploys/{id}` | 查询某次部署的结果，供客户端轮询 |
| `GET /v1/status/{service}` | 查看某服务当前运行状态，供人工排查 |
| `GET /v1/version` | 查询 agent 版本号，**唯一不需要证书的 `/v1/*` 接口**，见下文 |

`GET /healthz` 单独监听在 `127.0.0.1:8867`，不需要证书。

所有监听都设置了连接限制：请求头（含 TLS 握手）必须在 10 秒内收完，整个请求 30 秒，响应 30 秒，空闲连接 60 秒后关闭，请求头最大 16 KiB，请求体最大 64 KiB（并且只能是一个 JSON 值，后面只允许空白，否则 400）。`:8866` 同时打开的连接数默认最多 64 个（`serve --max-conns` 调整），超出的连接在内核队列里等待空位，而不是各占一个 goroutine。这是全局上限而非按来源 IP：放在四层透传代理后面时，所有连接的来源地址都是代理。慢速连接无法长期占用资源，因此 `:8866` 可以放在四层透传之后对外暴露。

同样的 `/v1/*` 接口还额外监听在本机 unix socket `/run/deploy-agent/deploy-agent.sock`（权限 0600，仅 root 可访问），不走 mTLS，供 `deploy-agent update`（见上文第 7 节）使用。

- socket 所在目录由 systemd unit 中的 `RuntimeDirectory=deploy-agent`、`RuntimeDirectoryMode=0700` 创建。旧版本安装的 unit 文件没有这两行（`install.sh` 不会覆盖已有 unit），此时 serve 会自己以 0700 创建 `/run/deploy-agent`；也可以手动把这两行加进 `/etc/systemd/system/deploy-agent.service` 后 `systemctl daemon-reload`
- serve 启动时检查 socket 的父目录：必须是真实目录（不接受符号链接）、属主为当前运行用户、并且 `mode & 0077 == 0`（组和其他用户没有任何权限），否则**拒绝启动**并给出原因。目录才是真正的访问边界，socket 本身的 0600 是第二层。agent 不修改进程 umask（umask 是进程级的，会影响其他 goroutine 写 sqlite 和 `.env`）
- 使用非默认 `--data-dir` 的安装保持原来的 `<data-dir>/agent.sock`，也可以用 `serve --socket <path>` 显式指定；两种情况都同样检查父目录，例如 `<data-dir>` 必须是 0700（`install.sh` 创建的 `/var/lib/deploy-agent` 本来就是 0700）
- 请求在 socket 上带有 `local` 身份，不受 `access` 绑定限制。所有访问检查默认拒绝：没有身份（既不是已登记证书，也不是本机 socket）的请求一律 403

`GET /v1/version` 是一个特例：它与其他 `/v1/*` 接口共享同一个 `:8866` 端口，但不要求客户端证书，直接明文 HTTP 访问即可：

```bash
curl http://10.0.0.5:8866/v1/version
# 1.0.0
```

用途：流水线在拿到客户端证书、甚至在下载客户端二进制之前，就需要知道目标 agent 的版本号，以便下载与之匹配的客户端版本（下文第 2 节的 `DEPLOY_AGENT_CLIENT_VERSION` 仍然必须固定，不能用 `latest`；`/v1/version` 只是帮助确定该固定成哪个值）。除这一个接口外，`:8866` 上的其他路径不受影响，依然强制 mTLS。

### 6. 日常运维

```bash
journalctl -u deploy-agent -f     # 查看日志（包括证书到期告警、反向校准告警等）
systemctl restart deploy-agent    # 修改清单后重启生效
```

修改清单或证书后，建议重启前先检查配置（类似 `nginx -t`，不影响正在运行的服务）：

```bash
sudo deploy-agent serve -t --bind 10.0.0.5:8866   # --bind 与 serve.env 中的 DEPLOY_AGENT_BIND 一致
# deploy-agent: configuration test is successful (1 services, server certificate valid until 2027-09-24 01:54:48)
```

`serve -t` 除了 serve 启动所需的检查（清单格式与路径、CA 与证书可加载），还会检查服务端证书当前有效、由本机 CA 签发、且 SAN 包含 `--bind` 的 IP。

Agent 重启时会自动把上次未完成的部署标记为 `interrupted`，并对每个服务做一次只读的反向校准（比对 `.env`、`last_healthy` 与 docker 实际运行状态，仅记录告警日志）。

### 7. 在 VPS 上手动更新服务（`deploy-agent update`）

开发人员已经 SSH 上机时，不需要走流水线也能直接触发一次部署：

```bash
sudo deploy-agent update -s web -t a1b2c3d
sudo deploy-agent update -s web -d sha256:<64 位十六进制>   # image_ref: digest 的服务
```

与流水线用的 `deploy-agent deploy` 相比，`update` 有两个不同点：

- 不需要 `--endpoint`：请求默认发给本机正在运行的 `deploy-agent serve` 进程
- 不需要 mTLS 凭据：走 serve 额外监听的本机 unix socket（`/run/deploy-agent/deploy-agent.sock`，权限 0600，目录 0700），信任根是主机文件权限而非证书，因此必须以 root 执行（`sudo`），这与 `docker.sock` 访问权等价于 root 的定位一致。升级后 serve 还没重启时，`update` 会自动改用旧位置 `/var/lib/deploy-agent/agent.sock`

`update` 复用与 `deploy` 相同的并发控制、幂等判断、失败自动回滚与退出码，输出格式也相同（见下文「Client 使用说明」第 4 节）；前提是 `deploy-agent serve` 正在运行，否则会报错并提示 `systemctl status deploy-agent`。

如果 `serve` 使用了非默认数据目录，需要加上 `--data-dir`（用来定位 socket 文件）；`serve` 用 `--socket` 指定了路径时，`update` 也传同样的 `--socket`：

```bash
sudo deploy-agent update -s web -t a1b2c3d --data-dir /custom/data-dir
```

### 8. 升级 agent 自身（`self-check` / `self-upgrade`）

注意与上一节的 `update`（更新业务服务）区分：`self-*` 命令只作用于 deploy-agent 自己，只能在部署 agent 的机器上执行，不提供 HTTP 接口。

检查是否有新版本（读取 GitHub 上最新 Release 的 tag）：

```bash
deploy-agent self-check
# current: 1.0.0
# latest:  1.1.0
# a new version is available; run `sudo deploy-agent self-upgrade`
```

退出码：`0` 已是最新，`10` 有新版本，`1` 出错，便于脚本判断。

升级并重启服务：

```bash
sudo deploy-agent self-upgrade
```

执行过程：

1. 下载最新版本到 `/usr/local/bin/` 下的临时文件，用同一次 Release 的 `deploy-agent.sha256` 校验
2. 用新二进制执行 `version` 与 `serve -t`，确认版本号正确、能通过当前配置；失败则中止，线上二进制不变
3. 若有正在进行的部署则拒绝升级，避免重启打断部署
4. 旧二进制保留为 `/usr/local/bin/deploy-agent.prev`，新二进制原子替换
5. 服务在运行时执行 `systemctl restart deploy-agent`，等待 `/healthz` 返回新版本；超时则自动换回旧二进制并再次重启。服务未运行时只替换二进制

常用参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--force` | `false` | 同版本重装、降级、本机为 `dev` 版本、或有部署进行中时仍然升级 |
| `--bind` | 读取 `--env-file` 中的 `DEPLOY_AGENT_BIND` | serve 的 `--bind`，用于 `serve -t` |
| `--env-file` | `/etc/deploy-agent/serve.env` | |
| `--manifest` / `--data-dir` | 与 serve 默认值相同 | serve 使用了非默认路径时需指定 |
| `--healthz-bind` | `127.0.0.1:8867` | serve 使用了非默认 `--healthz-bind` 时需指定 |
| `--health-timeout` | `1m` | 重启后等待新版本健康的时间 |

---

## 二、Client 使用说明（在 CI 流水线中调用）

### 1. 前置条件

- 已经在目标 Agent 上执行过 `deploy-agent ca issue`，并把输出的 PEM 证书包存入 CI 密钥，由流水线注入环境变量 `DEPLOY_AGENT_CREDENTIAL`
- 已确认要纳管的服务名与 `services.yaml` 中的逻辑服务名一致
- 已确定要部署的镜像 tag（建议用 commit SHA 或构建号，便于回溯；`test`、`latest` 这类可变 tag 也能部署，只是没法靠 tag 判断跑的是哪次构建）
- 已确定流水线使用的 `deploy-agent` 客户端版本号。建议固定版本号（**不要用 `latest`**），避免 agent 发布新版后流水线客户端行为跟着漂移

### 2. 在脚本任务中调用

参考模板见 [scripts/ci-deploy.sh](./scripts/ci-deploy.sh)，核心用法：

```bash
# 从 GitHub Releases 下载固定版本的客户端二进制
# 注意：下载会经过一次 302 重定向，curl 必须带 --location
curl --fail --location -o deploy-agent \
  "https://github.com/krwu/compose-deploy-agent/releases/download/${DEPLOY_AGENT_CLIENT_VERSION}/deploy-agent"
chmod +x deploy-agent

# 凭据只从环境变量读取，不通过命令行参数传递
export DEPLOY_AGENT_CREDENTIAL="<CI 密钥引用，内容为 ca issue 输出的多行 PEM>"

./deploy-agent deploy \
  -e "https://10.0.0.5:8866" \
  -s "web" \
  -t "${GIT_COMMIT_SHORT}"
```

命令行参数说明：

| 参数 | 简写 | 必填 | 说明 |
| --- | --- | --- | --- |
| `--endpoint` | `-e` | 是 | Agent 地址，形如 `https://<vps-ip>:8866`。主机名也可以是服务端证书 SAN 里的域名 |
| `--service` | `-s` | 是 | 逻辑服务名，须在目标机 `services.yaml` 白名单内 |
| `--tag` | `-t` | 与 `--digest` 二选一 | 目标镜像 tag，格式 `^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`。建议用不可变 tag（commit SHA、构建号），可变 tag 也能部署，但回滚与幂等判断都以 tag 为准 |
| `--digest` | `-d` | 与 `--tag` 二选一 | 目标镜像 digest，格式 `^sha256:[a-f0-9]{64}$`，只用于清单中配置了 `image_ref: digest` 的服务 |
| `--credential-env` | 无 | 否，默认 `DEPLOY_AGENT_CREDENTIAL` | 存放 PEM 证书包的环境变量名 |
| `--poll-timeout` | 无 | 否，默认 `10m` | 提交重试与状态轮询的总时间预算 |

### 3. 行为说明

- 提交后立即受理为异步任务，客户端自动轮询直到部署完成或超时
- 同一服务同一 tag 重复提交会复用同一次部署（幂等），不会重复重启
- 同一服务不同 tag 正在部署时返回冲突，客户端会在总超时内自动退避重试
- 部署失败（`start_failed`、`post_deploy_failed`）时 Agent 会自动尝试回滚，客户端只反映最终结果

### 4. 输出与退出码

执行结束后，结果以 JSON 打印到 stdout（凭据全程不会出现在输出中）：

```json
{
  "deploy_id": "5a330bbe...",
  "service": "web",
  "tag": "a1b2c3d",
  "result": "success",
  "rollback": "none",
  "image_id": "sha256:...",
  "exit_code": 0
}
```

| 退出码 | 含义 |
| --- | --- |
| `0` | 部署成功（`success` 或 `noop`） |
| `1` | 部署失败（`pull_failed` / `config_invalid` / `start_failed` / `post_deploy_failed` / `interrupted`） |
| `2` | 参数错误或认证失败（凭据无效、证书未登记/已吊销/未绑定该服务、service 不在白名单等） |
| `3` | 轮询总超时 |

流水线应将脚本任务的非零退出码视为部署失败，禁止把失败降级为警告。

---

## 参与开发

- 贡献指南（环境、代码结构、提交与 PR 要求）：[CONTRIBUTING.md](./CONTRIBUTING.md)
- 开源协议：[LICENSE](./LICENSE)

## 参考文档

- 端到端集成测试（需要 docker 环境）：[scripts/integration_test.sh](./scripts/integration_test.sh)
