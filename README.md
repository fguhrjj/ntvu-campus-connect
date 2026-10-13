# ntvu-campus-connect

南通职业大学校园网自动认证工具（CampusConnect）。Windows 托盘常驻，连上 `NTVU-R` 时自动完成门户认证：统一身份认证（学号 + 密码）→ 选择运营商 → 确认在线，掉线后自动重连。

单个 `CampusConnect.exe`，纯 HTTP 实现门户协议，不依赖浏览器、.NET、Node 或 PowerShell。

> 非学校官方项目，仅用于登录你自己的账号。门户结构变化时程序显示红色图标并按退避间隔重试，不会向白名单以外的地址发请求。

## 功能

- 触发：启动、连接 WLAN、系统唤醒、每 60 秒巡检、托盘“立即连接”。
- 失败退避 1 / 2 / 5 分钟；学号密码被拒、账号锁定、需要验证码等情况会暂停自动登录，直到在设置中重新保存（“立即连接”可手动绕过）。
- 托盘图标颜色：绿 = 在线，黄 = 网络/门户暂时不可用或外网不通，红 = 需要处理，灰 = 未连接校园网。悬停显示状态与最后检查时间，状态变化时弹气泡。
- 菜单：立即连接、打开认证页面、设置…、开机自启、打开日志、退出。

## 使用

1. 按下文“构建”得到 `CampusConnect.exe`，放到任意目录运行。
2. 首次运行会自动打开设置窗口：填写学号、密码，选择运营商（默认中国联通），保存后立即尝试连接。
3. 需要开机启动时，在设置或托盘菜单中勾选“开机自启”。

要求 Windows 10 及以上。若系统拒绝读取 WLAN 名称（位置权限），程序仍会通过门户跳转判断是否处于校园网。

| 内容 | 位置 |
|---|---|
| 配置 | `%APPDATA%\CampusConnect\config.json` |
| 日志（1 MB 轮转） | `%LOCALAPPDATA%\CampusConnect\campus.log` |
| 开机自启 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\CampusConnect` |

## 安全

- 密码用 Windows DPAPI（当前用户范围）加密后存储，只在提交前解密。
- 请求白名单：只访问 `nap.ntvu.edu.cn`、`authserver.ntvu.edu.cn`、`www.msftconnecttest.com` 和未认证时的 NAS 拦截页 `http://123.123.123.123/`；凭据只以 HTTPS POST 发往 `https://authserver.ntvu.edu.cn/authserver/login`。不走系统代理，TLS 校验开启。
- 日志脱敏：不记录密码、sessionId、ticket、Cookie 等；学号只保留后 4 位。

## 构建

需要 Go 1.27.1 以上（见 `go.mod`）。在 Linux / WSL 下交叉编译：

```sh
make windows        # 嵌入图标与清单，输出 CampusConnect.exe
make test           # go test ./...
make vet            # Linux 与 Windows 两个平台的 go vet
make windows-test   # 交叉编译各包测试，并在 Windows 上执行（WSL 互操作）
```

`make windows` 默认用 `go run` 拉取 `github.com/tc-hib/go-winres@v0.3.3`；无法访问 Go 模块代理时，可先安装再指定路径：`make windows GOWINRES=/path/to/go-winres`。

在 Windows 上直接构建（PowerShell）：

```powershell
go run github.com/tc-hib/go-winres@v0.3.3 make --in resources/winres.json --arch amd64 --out cmd/campusconnect/rsrc
go build -trimpath -ldflags "-H=windowsgui" -o CampusConnect.exe ./cmd/campusconnect
```

## 限制

不支持验证码识别、短信/扫码等其它认证方式、访客流程、多账号与非 Windows 平台。遇到需要验证码或手动处理的情况，用托盘“打开认证页面”在浏览器中登录一次即可。

## 文档

- 设计与实测的门户协议：[docs/superpowers/specs/2026-10-08-campus-autoconnect-design.md](docs/superpowers/specs/2026-10-08-campus-autoconnect-design.md)
- 实施计划：[docs/superpowers/plans/2026-10-10-campus-autoconnect.md](docs/superpowers/plans/2026-10-10-campus-autoconnect.md)

## 许可证

[MIT](LICENSE)：任何人都可以免费使用、修改、分发和商用，只需保留版权与许可声明。依赖的 `walk`、`win`、`golang.org/x/*` 等使用 BSD 许可，分发二进制时同样需附带它们的版权声明。
