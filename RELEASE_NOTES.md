# 绯狐 2.0.0

个人自用的 CodeBuddy / WorkBuddy 多账号网关，把账号池包装成 OpenAI 兼容 API，附 Web 管理面板。

本版基于 [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel)（该项目已于 2026-10-21 停止维护），在其基础上做两类改动：**修既有缺陷**、**把通道指纹一致性做扎实**。

## 下载哪个

| 文件 | 适用 |
|---|---|
| `feihu-v2.0.0-windows-amd64.zip` | Windows x64，解压即用 |
| `feihu-v2.0.0-linux-amd64.tar.gz` | Linux x64（含主流云服务器） |
| `feihu-v2.0.0-linux-arm64.tar.gz` | Linux ARM64（含 Oracle 免费实例、树莓派） |
| `SHA256SUMS.txt` | 校验下载完整性 |

每个包内含：`feihu`（主服务 + 面板）、`login`（加账号）、`credit` / `signin`（命令行工具）、`config.example.json`、`README.md`、`scripts/`（通道探针、真机身份迁移、发布构建）。

## 相对原始项目的改动

**上游收紧反代通道：报文层与设备身份层**

原始项目把客户端版本写死为 5.5.4（UA）与 5.5.6（桌面指纹），而上游桌面端已到 5.8.x，且同一实例跨端点自报两个不同版本。上游一旦做「UA 版本 vs 账号活跃版本」一致性检查，报文其余部分再完美也会被判非官方客户端（`11-128 request illegal`）。

本版把版本段改为从**本机真实安装的官方客户端** `last-launch.json` 学习（优先级：`upstream.client_version` 显式配置 > 跟随官方客户端 > 内置兜底），两处收敛为同一来源，官方升级后自动跟上。启动日志会打印实际采用的版本与来源文件。

设备标识同理：原始项目一律用 `sha256(uid)` 伪造 `machineId` / `qimei36`，本版支持从本机官方客户端数据目录注入真值，出站优先用真值。工具：`scripts/mine-identity.ps1`。

**上游可见的指纹泄漏**

原始项目发给上游的会话/请求 ID 前缀写死 `wb2api-`，任务标题里写着 `wb2api 自动化` —— 上游能看到网关为每个会话生成的 ID 都带同一品牌前缀。本版已去品牌化（改为 `cmb-` 形态、标题改中性文案）。

**其他修复**

- 版本探测走无锁读（原实现若引入会成高并发下的全局锁热点）
- 真机设备标识注入默认拒绝多账号共用（同一 device-id 关联多个账号是批量小号最典型的特征）
- `mine-identity.ps1` 处理 Windows PowerShell 5.1 的编码问题（UTF-8 BOM）
- 本地发布线 `scripts/build-release.ps1`：一次产出三平台包 + 校验和，不依赖远程 CI

## 快速开始

```bash
# 1. 解压后进入目录
./feihu -config config.json          # 首次运行自动生成 config.json（含随机 api_key，日志打印一次）

# 2. 加账号（浏览器 OAuth）
./login url                          # 打开输出的链接，用你的 CodeBuddy/WorkBuddy 账号授权
./login poll                         # 凭证落到 auths/workbuddy-<uid>.json

# 3. 打开面板
# http://127.0.0.1:7863/panel/
```

国际版账号加 `--realm=global`。

## 使用前必须知道的三件事

**一、仅限自用。** 本项目是给个人管理自己的账号用的（签到、保活、给自己的本地工具提供一个 OpenAI 兼容入口）。不支持也不授权批量小号分发额度、二次打包或收费售卖。

**二、账号风险自负。** 上游正在收紧对非官方客户端的识别与拦截。使用非官方客户端可能触发账号风控甚至封禁，后果由使用者承担。本版降低了被识别的概率，但不提供任何保证。

**三、凭证是本机的。** `auths/` 目录里是**明文** accessToken / refreshToken。不要外传该目录，不要把 `auths/` 或 `config.json` 提交到任何仓库。`scripts/mine-identity.ps1` 会读取本机官方客户端数据目录（`~/.workbuddy`）的 device-id 与 qimei36 —— 它只在本机读、只写进本机的 auth 文件，不联网外传。

## 已知限制

- 传输层指纹（TLS ClientHello / HTTP2 SETTINGS）尚未对齐官方客户端：Go 标准库与 Electron BoringSSL 的指纹差异仍然存在。`scripts/chanprobe.py` 是用于定位该层是否被校验的差分探针，需要自备账号运行。
- 若上游返回 `11140 request illegal`，那是账号级限制，改协议无效，只能换号或向官方申诉。
- `config.json` 里 `listen` / `auth_dir` / `state_file` / `upstream.*` / `upstash` 改动需重启；面板会提示。

## 许可

MIT。保留 [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) 与 [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel) 的原始版权声明，详见 `LICENSE`。
