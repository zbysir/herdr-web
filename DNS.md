# 各家 DNS 的 token 怎么拿

herdr-web 自己签证书用的是 **DNS-01**：它需要往你的域名下临时加一条 TXT 记录，验证完删掉。
所以要给它一份能改这个域名的 DNS 凭据。

配好之后长这样（放 `.env`，`make run` 会读）：

```bash
HERDR_WEB_HOSTNAME=herdr.example.com
HERDR_WEB_ACME_DNS=cloudflare
HERDR_WEB_ACME_STAGING=1        # 第一次一定先开，见下面「先用测试环境」
HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=xxxxx
```

**变量名都带 `HERDR_WEB_` 前缀，云厂商那几个也一样。** 这不是为了整齐：
`herdr-web service install` 抄进 plist / unit 的就是「所有 `HERDR_WEB_*` + 一张短白名单」，
光秃秃的 `CLOUDFLARE_DNS_API_TOKEN` 两头都不占 —— 装出来的服务签不出证书，而且要等到
第一次签发（或者三个月后第一次续期）才炸。老写法仍然认（lego 自己会读），两个都给的话
**带前缀的赢**。

## 三条通用原则

**一、只给 DNS 的权限，范围限到那一个域名。** 这份凭据要放在一台**跑着 coding agent** 的
机器上。给全账号权限的话，一次 prompt injection 就等于交出整个云账号；只给 DNS 的话，最坏
情况是有人改了你的解析 —— 差好几个量级。所有平台都支持建子用户/细粒度 token，别偷懒用
主账号的密钥。

**二、放 `.env`，别写进 shell 的 rc。** 写进 `~/.zshrc` 的话，你在终端里跑的每个进程都能
看到它，包括 agent。`.env` 只有 herdr-web 自己读，而且已经在 `.gitignore` 里。
（带了前缀之后 rc 里 export 也**能**被 `service install` 抄进去 —— 能用不等于该用，
上面这条理由一条都没少。）

**三、这些变量不会漏进网页里的终端。** PTY 的子进程环境会被 `dropEnv` 清掉这一批
（`internal/server/pty.go`）—— 否则终端里的 agent `echo` 一下就读走了。清掉不影响你自己用：
PTY 起的是登录 shell，会重新 source 你的 profile，你在 rc 里 export 的那些照样在。

## 先用测试环境

**第一次一定先加 `HERDR_WEB_ACME_STAGING=1`。** Let's Encrypt 的正式环境对同一组域名
**一周只给 5 张证书**，配错了反复试会把自己锁一周。测试环境签出来的证书浏览器不认（会报
警告），但足够验证「凭据对不对、DNS 权限够不够」。跑通了再去掉这个变量，重启一次即可。

签好的证书在 `~/.herdr-web/data/acme/`，测试环境那张单独叫 `*.staging.crt` —— 两套并存，
所以「用 staging 试一次」不会盖掉线上那张真证书，切回正式也不用手动删文件。

---

## Cloudflare

```bash
HERDR_WEB_CLOUDFLARE_DNS_API_TOKEN=...
```

1. 右上头像 → **My Profile** → **API Tokens** → **Create Token**
2. 用 **Edit zone DNS** 模板
3. Permissions 保持 `Zone` / `DNS` / `Edit`
4. Zone Resources 改成 **Include → Specific zone → 你的域名**（默认是所有 zone，收窄它）
5. 创建后 token 只显示一次

⚠️ 别用 **Global API Key**（那是账号全权限，虽然 lego 也认 `CLOUDFLARE_EMAIL` +
`CLOUDFLARE_API_KEY`，也就是 `HERDR_WEB_CLOUDFLARE_EMAIL` + `HERDR_WEB_CLOUDFLARE_API_KEY`，
但等于把整个 Cloudflare 账号交出去）。

⚠️ 记录如果是**橙云**（代理状态），TLS 会在 Cloudflare 解密一次 —— 签证书不受影响，但那
就多了一个能看到你终端内容的第三方。想端到端就调成灰云（DNS only）。

## 阿里云（alidns）

```bash
HERDR_WEB_ALICLOUD_ACCESS_KEY=...
HERDR_WEB_ALICLOUD_SECRET_KEY=...
```

1. 控制台 → **访问控制 RAM** → 用户 → **创建用户**，勾「使用永久 AccessKey 访问」
2. 给它授权，两种选法：
   - 省事：系统策略 **AliyunDNSFullAccess**
   - 收窄：自定义策略，只给这几个 Action，Resource 限到那个域名
     ```json
     {"Version":"1","Statement":[{"Effect":"Allow","Action":[
       "alidns:DescribeDomainRecords","alidns:DescribeSubDomainRecords",
       "alidns:AddDomainRecord","alidns:DeleteDomainRecord"],
       "Resource":"acs:alidns:*:*:domain/example.com"}]}
     ```
3. AccessKey Secret 只显示一次

⚠️ 别用主账号的 AccessKey —— 那把钥匙能开你阿里云上的所有东西。

## 腾讯云（tencentcloud）

```bash
HERDR_WEB_TENCENTCLOUD_SECRET_ID=...
HERDR_WEB_TENCENTCLOUD_SECRET_KEY=...
```

1. 控制台 → **访问管理 CAM** → 用户 → **新建用户** → 自定义创建，勾「编程访问」
2. 授权策略：**QcloudDNSPodFullAccess**（收窄的话自定义只给 `dnspod:CreateRecord` /
   `DeleteRecord` / `DescribeRecordList`）
3. SecretKey 只显示一次

⚠️ 域名解析在腾讯云是 DNSPod 那套。如果你的域名还挂在独立的老 DNSPod 账号下、没和腾讯云
账号打通，这组密钥会调不到 —— 那种情况先在 DNSPod 控制台把账号关联上。

## AWS Route 53

```bash
HERDR_WEB_AWS_ACCESS_KEY_ID=...
HERDR_WEB_AWS_SECRET_ACCESS_KEY=...
HERDR_WEB_AWS_REGION=us-east-1
```

1. **IAM** → 用户 → 创建用户 → 给它一个内联策略：
   ```json
   {"Version":"2012-10-17","Statement":[
     {"Effect":"Allow","Action":["route53:GetChange"],"Resource":"arn:aws:route53:::change/*"},
     {"Effect":"Allow","Action":["route53:ListHostedZonesByName"],"Resource":"*"},
     {"Effect":"Allow","Action":["route53:ChangeResourceRecordSets","route53:ListResourceRecordSets"],
      "Resource":"arn:aws:route53:::hostedzone/你的ZONEID"}]}
   ```
2. 创建 Access key（Security credentials 页）

⚠️ `HERDR_WEB_AWS_REGION` 随便填一个合法值就行 —— Route 53 是全局服务，但 SDK 一定要一个 region，
不给会报一个不知所云的错。

⚠️ 机器上本来就有 `~/.aws/credentials` 或 `AWS_PROFILE` 的话，lego 也会用它们（AWS SDK
自己那套查找链，不经过我们的前缀）。那就意味着
**签证书用的是你日常那把全权限密钥** —— 建议还是显式给这三个变量，用专门的最小权限用户。

## DigitalOcean

```bash
HERDR_WEB_DO_AUTH_TOKEN=...
```

1. 左下 **API** → **Tokens** → **Generate New Token**
2. 新版有细粒度 scope：只勾 **domain** 的 read + write（别给 full access）
3. token 只显示一次

## 华为云（huaweicloud）

```bash
HERDR_WEB_HUAWEICLOUD_ACCESS_KEY_ID=...
HERDR_WEB_HUAWEICLOUD_SECRET_ACCESS_KEY=...
HERDR_WEB_HUAWEICLOUD_REGION=cn-north-4
```

1. 控制台 → **统一身份认证 IAM** → 用户 → 创建用户，访问方式选「编程访问」
2. 授权：**DNS FullAccess**（收窄的话自定义策略只给记录集的增删查）
3. 我的凭证 → **访问密钥** → 新增，下载那个 csv（只能下一次）

⚠️ `HERDR_WEB_HUAWEICLOUD_REGION` 必填，写你账号常用的那个（`cn-north-4` / `cn-east-3` …）。DNS 本身
是全局服务，但 SDK 要 region 才能签请求。

---

## 换一家没编译进来的怎么办

现在编译进来的是这六家。lego 支持一百五十多家，加一家是三处
（`internal/acme/acme.go` 里 `envHint` 加一行、`newDNS` 加一个 `case`，
`internal/acme/env.go` 里 `dnsNamespaces` 加一行 —— 少了最后那个，带前缀的凭据会被
当成没配）。

之所以不全都编译进来：那个聚合包会把一百多家的 SDK 全带上，二进制涨一个数量级
（实测每家的边际成本只有 0.5–1.8 MB，但一百五十家就不是这个量级了）。

**或者干脆不用内置的**：你已经在用 `acme.sh` / `certbot` / `lego` 命令行的话，让它们照常
续期，herdr-web 只管指过去 ——

```bash
HERDR_WEB_TLS_CERT=/path/fullchain.pem
HERDR_WEB_TLS_KEY=/path/privkey.pem
```

这条路也有热重载：你的续期脚本换了文件，十秒内自己捡起来，不用重启（重启会断掉所有连着
的终端）。

## 出错了看这里

- **`不认识的 DNS 服务商 "aliyun"`** → 名字是 `alidns`。错误信息里会列出全部六个。
- **`some credentials information are missing: XXX`** → 那几个变量没读到。**lego 报的是
  没有前缀的名字**（`CLOUDFLARE_DNS_API_TOKEN`），我们在下一行会补一句带前缀的 ——
  照带前缀的那个配。然后检查是不是写在 `.env` 里而不是别的地方，以及 `make run` 有没有
  真的读到那个文件（启动时会打一行 `→ 配置：./.env`）。
- **在终端里 export 了却不生效** → 少了 `HERDR_WEB_` 前缀。`herdr-web service install`
  的输出里会把抄进去的变量全列出来（凭据只打星号和长度），没看到名字就是没抄进去。
- **一直卡在 DNS 验证** → 权限不够（能查不能写），或者域名的 NS 其实不在这家。
  `dig +short TXT _acme-challenge.你的域名` 能看到那条临时记录说明写进去了。
- **`too many certificates already issued`** → 撞上正式环境一周 5 张的限制了。只能等，
  或者先用 `HERDR_WEB_ACME_STAGING=1` 把流程调通。
