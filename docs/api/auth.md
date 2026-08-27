# 认证管理 API

[返回目录](./README.md)

OIDC 完整调用流程见 [../OIDC认证调用流程.md](../OIDC认证调用流程.md)。本文档作为端点参考。企业 SSO（`weknora-web`）走同一组 BFF 端点：授权码 + PKCE 在服务端换码，按 `sub` 建号；业务票仍是 WeKnora JWT。空间 ACL 不在 OAuth 票里。

## 说明

WeKnora 的 `/auth/*` 端点本身**不需要 X-API-Key**，但部分端点需要在 `Authorization: Bearer <token>` 头中携带由 `/auth/login` 或 `/auth/oidc/callback` 返回的 JWT：

| 端点 | 鉴权方式 |
| --- | --- |
| `/auth/register` `/auth/login` | 无 |
| `/auth/oidc/config` `/auth/oidc/url` `/auth/oidc/callback` `/auth/sso/exchange` | 无（exchange 的 Bearer 是 SSO 资源票，不是 WeKnora JWT） |
| `/auth/refresh` | refresh_token（请求体携带） |
| `/auth/validate` `/auth/me` `/auth/logout` `/auth/change-password` | Bearer JWT |

注册接口可通过环境变量 `DISABLE_REGISTRATION=true` 关闭。

## 端点一览

| 方法 | 路径                       | 描述                                       |
| ---- | -------------------------- | ------------------------------------------ |
| POST | `/auth/register`           | 用户注册                                   |
| POST | `/auth/login`              | 用户登录                                   |
| GET  | `/auth/oidc/config`        | 获取 OIDC 配置元数据                       |
| GET  | `/auth/oidc/url`           | 获取 OIDC 授权链接                         |
| GET  | `/auth/oidc/callback`      | OIDC 授权回调（由 IdP 重定向触发）         |
| POST | `/auth/sso/exchange`       | Hermes：`aud=weknora` SSO 票换 WeKnora JWT |
| POST | `/auth/refresh`            | 用 refresh_token 换新的 access_token       |
| GET  | `/auth/validate`           | 验证 JWT 有效性                            |
| POST | `/auth/logout`             | 退出登录                                   |
| GET  | `/auth/me`                 | 获取当前用户信息                           |
| POST | `/auth/change-password`    | 修改密码                                   |

---

## POST `/auth/register` - 用户注册

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 校验                       | 说明      |
| -------- | ------ | ---- | -------------------------- | --------- |
| username | string | 是   | 长度 2-50                   | 用户名    |
| email    | string | 是   | 邮箱格式                   | 邮箱      |
| password | string | 是   | 最少 6 位                   | 密码      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/register' \
--header 'Content-Type: application/json' \
--data '{
    "username": "alice",
    "email": "alice@example.com",
    "password": "secret123"
}'
```

**响应**（201 Created）:

```json
{
    "success": true,
    "message": "Registration successful",
    "user": {
        "id": "usr-...",
        "username": "alice",
        "email": "alice@example.com",
        "tenant_id": 1,
        "is_active": true,
        "created_at": "2026-05-11T10:00:00+08:00",
        "updated_at": "2026-05-11T10:00:00+08:00"
    },
    "tenant": {
        "id": 1,
        "name": "alice's workspace",
        "api_key": "sk-..."
    }
}
```

**错误**: 注册被禁用 → 403；参数校验失败 → 400。

---

## POST `/auth/login` - 用户登录

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 说明          |
| -------- | ------ | ---- | ------------- |
| email    | string | 是   | 注册邮箱      |
| password | string | 是   | 密码          |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/login' \
--header 'Content-Type: application/json' \
--data '{
    "email": "alice@example.com",
    "password": "secret123"
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Login successful",
    "user": { "id": "usr-...", "username": "alice", "email": "alice@example.com" },
    "tenant": { "id": 1, "name": "alice's workspace", "api_key": "sk-..." },
    "token": "eyJhbGciOi...",
    "refresh_token": "eyJhbGciOi..."
}
```

**错误**: 邮箱或密码错误 → 401；账号被禁用 → 403。

---

## GET `/auth/oidc/config` - 获取 OIDC 配置元数据

返回 OIDC 是否启用以及 Provider 显示名，前端登录页据此决定是否展示 OIDC 登录按钮。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/config'
```

**响应**:

```json
{
    "success": true,
    "enabled": true,
    "provider_display_name": "WeKnora SSO"
}
```

---

## GET `/auth/oidc/url` - 获取 OIDC 授权链接

返回前端应跳转的 OIDC IdP 授权页 URL 与状态码。

**查询参数**:

| 字段       | 类型   | 必填 | 说明                                                    |
| ---------- | ------ | ---- | ------------------------------------------------------- |
| redirect   | string | 否   | 登录成功后前端期望落地的路径（如 `/dashboard`），透传到 state |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/url?redirect=%2Fdashboard'
```

**响应**:

```json
{
    "success": true,
    "provider_display_name": "WeKnora SSO",
    "authorization_url": "https://idp.example.com/oauth/authorize?client_id=...&state=...",
    "state": "abcdef..."
}
```

---

## GET `/auth/oidc/callback` - OIDC 授权回调

由 IdP 在用户授权后重定向到此端点。一般不需要客户端代码直接调用——它的作用是把登录结果通过浏览器 hash 传回前端首页。

**查询参数**:

| 字段              | 类型   | 必填 | 说明                          |
| ----------------- | ------ | ---- | ----------------------------- |
| code              | string | 是   | IdP 颁发的 authorization code |
| state             | string | 是   | 与 `/auth/oidc/url` 返回值一致 |
| error             | string | 否   | IdP 返回的错误标识            |
| error_description | string | 否   | IdP 返回的错误详情            |

**响应**：始终返回 `302 Found`，跳转到 `/`，并把结果编码进 URL hash：

- 成功：`/#oidc_result=<base64url(JSON payload)>`，其中 payload 包含 `success` / `user` / `tenant` / `token` / `refresh_token` / `is_new_user`，与登录响应一致。
- 失败：`/#oidc_error=<reason>[&oidc_error_description=<message>]`，常见 reason 包括 `invalid_state`、`missing_code`、`login_failed`、`payload_encode_failed`。

---

## GET `/auth/oidc/start` - 发起 OIDC 登录（直接 302）

与 `/auth/oidc/url` 不同，此端点**直接 302 重定向**到 OIDC Provider 的授权页，不返回 JSON，因此无需前端 JS 介入。适用于外部平台（如企业门户 / Nexus）直接给出一个链接即可触发 OIDC 授权码流程，借助 IdP 的 SSO session 实现免再次输密码。

回调地址由后端根据请求自身的 origin（`<scheme>://<host>/api/v1/auth/oidc/callback`）自动构造，无需调用方提供。

---

## POST `/auth/sso/exchange` - Hermes 用 SSO 资源票换 WeKnora JWT

给 Hermes（或其它持有 `aud=weknora` 短票的客户端）。**不是**网页 OIDC 回调。WeKnora 业务接口仍只认自己签发的 Bearer。

**请求头**: `Authorization: Bearer {SSO access token}`，且该票必须是 RS256、`aud` 为单个字符串 `weknora`（或 `OIDC_AUTH_RESOURCE_AUDIENCE`）、`iss` 与配置的 issuer 一致、未过期。拒绝 `aud=sso` 登录票。不要把 token 放进 JSON body。

**成功 200**：与 `/auth/login` 相同，含 `token` / `refresh_token` / `user` / `active_tenant` / `memberships`。服务端会 JWKS 验签并 introspect；`active: false` 为 401。之后用 WeKnora `token` 调 `GET /tenants` 和检索，并带 `X-Tenant-ID`。没有成员关系则没有知识库。

登录/换票只打 `QueryEmployeeDirectory` 查**当前这个人**的部门（不拉整棵组织树）。用返回的部门在本地 upsert 部门/企微身份，再绑定 Source ACL；其它已经有这些部门记录的空间会把人加为 viewer。整棵通讯录仍走空间里的企微同步，不是 SSO 登录协议。

**失败**：OIDC 未启用 403；验签失败 / 错 audience / 过期 / 账号停用 401。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/oidc/start'
```

**响应**：`302 Found`，`Location` 指向 IdP 授权页（含 `client_id` / `state` / `redirect_uri` / `scope`）。

> 登录成功后的回调行为与 `/auth/oidc/callback` 一致：302 回前端首页并把登录结果编码进 URL hash。当前登录后固定落到默认首页 `/platform/knowledge-bases`（直达指定业务页的 `next` 参数为未来扩展）。

---

## POST `/auth/refresh` - 刷新令牌

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 说明              |
| ------------- | ------ | ---- | ----------------- |
| refreshToken  | string | 是   | 登录时颁发的 refresh_token |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/refresh' \
--header 'Content-Type: application/json' \
--data '{
    "refreshToken": "eyJhbGciOi..."
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Token refreshed successfully",
    "access_token": "eyJhbGciOi...",
    "refresh_token": "eyJhbGciOi..."
}
```

**错误**: refresh_token 无效或过期 → 401。

---

## GET `/auth/validate` - 验证 JWT

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/validate' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**:

```json
{
    "success": true,
    "valid": true,
    "user_id": "usr-...",
    "tenant_id": 1
}
```

无效 token 返回 401。

---

## POST `/auth/logout` - 退出登录

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/auth/logout' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**: `{ "success": true, "message": "Logged out successfully" }`

---

## GET `/auth/me` - 获取当前用户信息

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/me' \
--header 'Authorization: Bearer eyJhbGciOi...'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "user": {
            "id": "usr-...",
            "username": "alice",
            "email": "alice@example.com",
            "avatar": "",
            "tenant_id": 1,
            "is_active": true,
            "can_access_all_tenants": false,
            "created_at": "2026-05-11T10:00:00+08:00",
            "updated_at": "2026-05-11T10:00:00+08:00"
        },
        "tenant": {
            "id": 1,
            "name": "My Workspace"
        },
        "memberships": [
            {
                "tenant_id": 1,
                "tenant_name": "My Workspace",
                "role": "owner"
            }
        ],
        "tenant_required": false,
        "capabilities": {
            "can_create_tenant": false,
            "auto_accept_invitation": false
        }
    }
}
```

`capabilities` 供 SPA 读取部署级开关，无需调用超管设置 API：

| 字段 | 说明 |
| ---- | ---- |
| `can_create_tenant` | 当前用户是否可自助创建空间 |
| `auto_accept_invitation` | 全局 `tenant.auto_accept_invitation`：邮箱邀请已注册用户时是否直接加入（无需收件箱确认） |

---

## POST `/auth/change-password` - 修改密码

修改当前用户的登录密码。新密码须满足 **8–32 位**且**同时包含字母与数字**；不能与当前密码相同。成功后**所有会话被撤销**，需使用新密码重新登录。

**参数说明（请求体）**:

| 字段          | 类型   | 必填 | 校验    | 说明      |
| ------------- | ------ | ---- | ------- | --------- |
| old_password  | string | 是   |          | 当前密码  |
| new_password  | string | 是   | 8–32 位，须含字母与数字，且不同于旧密码 | 新密码    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/auth/change-password' \
--header 'Authorization: Bearer eyJhbGciOi...' \
--header 'Content-Type: application/json' \
--data '{
    "old_password": "secret123",
    "new_password": "newsecret456"
}'
```

**响应**: `{ "success": true, "message": "Password changed successfully" }`

**错误**（400）:

| `error.details`       | 含义                         |
| --------------------- | ---------------------------- |
| `invalid_old_password` | 当前密码不正确               |
| `password_policy`      | 新密码不满足长度/复杂度要求  |
| `same_password`        | 新密码与当前密码相同         |
