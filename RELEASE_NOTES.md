# 绯狐 2.0.0

个人维护的 CodeBuddy / WorkBuddy 多账号网关，把账号池包装成 OpenAI 兼容 API，附 Web 管理面板。

基于 [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel)。上游已于 2026-10-21 停止维护，本分支由个人接手维持可用，跟随上游协议变化继续更新。

## 下载哪个

| 文件 | 适用 |
|---|---|
| `feihu-v2.0.0-windows-amd64.zip` | Windows x64，解压即用 |
| `feihu-v2.0.0-linux-amd64.tar.gz` | Linux x64（含主流云服务器） |
| `feihu-v2.0.0-linux-arm64.tar.gz` | Linux ARM64（含 Oracle 免费实例、树莓派） |
| `SHA256SUMS.txt` | 校验下载完整性 |

每个包内含：`feihu`（主服务 + 面板）、`login`（加账号）、`credit` / `signin`（命令行工具）、`config.example.json`、`README.md`、`scripts/`（运维脚本）。

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

功能、配置项、API 端点与管理面板的完整说明见仓库内的 `README.md`。

## 使用前必须知道的三件事

**一、仅限自用。** 本项目是给个人管理自己的账号用的（签到、保活、给自己的本地工具提供一个 OpenAI 兼容入口）。不支持也不授权批量小号分发额度、二次打包或收费售卖。

**二、账号风险自负。** 使用非官方客户端可能触发上游账号风控，后果由使用者承担。

**三、凭证是本机的。** `auths/` 目录里是**明文** accessToken / refreshToken。不要外传该目录，不要把 `auths/` 或 `config.json` 提交到任何仓库。

## 已知限制

- `config.json` 里 `listen` / `auth_dir` / `state_file` / `upstream.*` / `upstash` 改动需重启；面板会提示。
- 上游接口随时可能变化，本分支跟随更新但不提供任何可用性保证。

## 许可

MIT。保留 [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) 与 [linguo2625469/workbuddy2api-panel](https://github.com/linguo2625469/workbuddy2api-panel) 的原始版权声明，详见 `LICENSE`。
