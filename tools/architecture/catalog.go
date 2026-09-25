package architecture

// 依赖表仅保存业务协作和技术库选择；目录角色与通用约束由 policy.go 统一解释。

var roleLibraries = map[string]dependencySet{
	"assembly": {Production: `entgo.io/ent/dialect entgo.io/ent/dialect/sql github.com/gin-gonic/gin github.com/google/uuid
github.com/google/wire github.com/imroc/req/v3 github.com/lib/pq github.com/redis/go-redis/v9
go.uber.org/zap golang.org/x/term gopkg.in/yaml.v3`, Tests: `github.com/coder/websocket github.com/stretchr/testify/assert github.com/stretchr/testify/require
github.com/testcontainers/testcontainers-go/modules/postgres
github.com/testcontainers/testcontainers-go/modules/redis github.com/tidwall/gjson
go.uber.org/zap/zaptest/observer modernc.org/sqlite`},
	"core": {Production: `github.com/alitto/pond/v2 github.com/cespare/xxhash/v2 github.com/dgraph-io/ristretto
github.com/go-webauthn/webauthn/protocol github.com/go-webauthn/webauthn/webauthn
github.com/golang-jwt/jwt/v5 github.com/google/uuid github.com/patrickmn/go-cache
github.com/pquerna/otp github.com/pquerna/otp/totp github.com/robfig/cron/v3
github.com/shopspring/decimal github.com/tidwall/gjson github.com/tidwall/sjson
github.com/tiktoken-go/tokenizer go.uber.org/zap golang.org/x/crypto/bcrypt golang.org/x/image/draw
golang.org/x/image/webp golang.org/x/net/http/httpguts golang.org/x/net/publicsuffix
golang.org/x/sync/errgroup golang.org/x/sync/singleflight`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/redis/go-redis/v9 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/testcontainers/testcontainers-go/modules/redis`},
	"fixture": {Production: `entgo.io/ent/dialect entgo.io/ent/dialect/sql github.com/alicebob/miniredis/v2
github.com/gin-gonic/gin github.com/lib/pq github.com/redis/go-redis/v9
github.com/stretchr/testify/require github.com/stretchr/testify/suite
github.com/testcontainers/testcontainers-go/modules/postgres
github.com/testcontainers/testcontainers-go/modules/redis modernc.org/sqlite`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/fxamacker/cbor/v2 github.com/golang-jwt/jwt/v5
github.com/google/uuid github.com/patrickmn/go-cache github.com/pquerna/otp/totp
github.com/stretchr/testify/assert github.com/stripe/stripe-go/v85`},
	"http": {Production: `github.com/coder/websocket github.com/gin-gonic/gin github.com/gin-gonic/gin/binding
github.com/google/uuid github.com/google/wire github.com/gorilla/websocket
github.com/klauspost/compress/zstd github.com/tidwall/gjson github.com/tidwall/sjson go.uber.org/zap
golang.org/x/net/http/httpguts golang.org/x/net/http2 golang.org/x/sync/singleflight`, Tests: `github.com/cespare/xxhash/v2 github.com/imroc/req/v3 github.com/stretchr/testify/assert
github.com/stretchr/testify/require go.uber.org/zap/zaptest/observer`},
	"infra": {Production: `github.com/andybalholm/brotli github.com/imroc/req/v3 github.com/klauspost/compress/zstd
github.com/lib/pq github.com/redis/go-redis/v9 github.com/refraction-networking/utls
github.com/zeromicro/go-zero/core/collection go.uber.org/zap go.uber.org/zap/zapcore
golang.org/x/net/http2 golang.org/x/net/proxy gopkg.in/natefinch/lumberjack.v2`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/alicebob/miniredis/v2 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/testcontainers/testcontainers-go/modules/redis
github.com/tidwall/gjson`},
	"postgres": {Production: `entgo.io/ent/dialect entgo.io/ent/dialect/sql entgo.io/ent/dialect/sql/sqljson
github.com/go-webauthn/webauthn/webauthn github.com/google/uuid github.com/lib/pq
github.com/patrickmn/go-cache golang.org/x/sync/errgroup`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/redis/go-redis/v9 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/stretchr/testify/suite
github.com/testcontainers/testcontainers-go/modules/postgres
github.com/testcontainers/testcontainers-go/modules/redis modernc.org/sqlite`},
	"provider": {Production: `github.com/alibabacloud-go/captcha-20230305/client
github.com/alibabacloud-go/darabonba-openapi/v2/utils github.com/alibabacloud-go/tea/dara
github.com/alibabacloud-go/tea/tea github.com/aws/aws-sdk-go-v2/aws
github.com/aws/aws-sdk-go-v2/aws/signer/v4 github.com/aws/aws-sdk-go-v2/config
github.com/aws/aws-sdk-go-v2/credentials github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager
github.com/aws/aws-sdk-go-v2/service/s3 github.com/coder/websocket
github.com/go-webauthn/webauthn/protocol github.com/go-webauthn/webauthn/webauthn
github.com/golang-jwt/jwt/v5 github.com/google/uuid github.com/imroc/req/v3
github.com/redis/go-redis/v9 github.com/robfig/cron/v3 github.com/shirou/gopsutil/v4/cpu
github.com/shirou/gopsutil/v4/disk github.com/shirou/gopsutil/v4/mem github.com/shopspring/decimal
github.com/smartwalle/alipay/v3 github.com/spf13/viper github.com/stripe/stripe-go/v85
github.com/stripe/stripe-go/v85/webhook
github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/captcha/v20190722
github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common
github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile github.com/tidwall/gjson
github.com/tidwall/sjson github.com/wechatpay-apiv3/wechatpay-go/core
github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers
github.com/wechatpay-apiv3/wechatpay-go/core/notify
github.com/wechatpay-apiv3/wechatpay-go/core/option
github.com/wechatpay-apiv3/wechatpay-go/services/payments
github.com/wechatpay-apiv3/wechatpay-go/services/payments/h5
github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi
github.com/wechatpay-apiv3/wechatpay-go/services/payments/native
github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic
github.com/wechatpay-apiv3/wechatpay-go/utils go.uber.org/zap google.golang.org/api/idtoken`, Tests: `github.com/gin-gonic/gin github.com/stretchr/testify/assert github.com/stretchr/testify/require
github.com/stretchr/testify/suite github.com/testcontainers/testcontainers-go/modules/redis
golang.org/x/crypto/curve25519 golang.org/x/crypto/nacl/box golang.org/x/net/http2
google.golang.org/api/option`},
	"pure": {Production: `github.com/tidwall/gjson github.com/tidwall/sjson golang.org/x/mod/semver
golang.org/x/net/http/httpguts golang.org/x/sync/singleflight`, Tests: "github.com/stretchr/testify/assert github.com/stretchr/testify/require"},
	"redis": {Production: "github.com/redis/go-redis/v9", Tests: `github.com/alicebob/miniredis/v2 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/stretchr/testify/suite
github.com/testcontainers/testcontainers-go/modules/redis`},
	"schema": {Production: `entgo.io/ent entgo.io/ent/dialect entgo.io/ent/dialect/entsql entgo.io/ent/dialect/sql
entgo.io/ent/schema entgo.io/ent/schema/edge entgo.io/ent/schema/field entgo.io/ent/schema/index
entgo.io/ent/schema/mixin`, Tests: `entgo.io/ent/dialect/sql/schema entgo.io/ent/entc/load github.com/stretchr/testify/require
modernc.org/sqlite`},
	"upstream": {Production: `github.com/aws/aws-sdk-go-v2/aws github.com/aws/aws-sdk-go-v2/aws/signer/v4
github.com/cespare/xxhash/v2 github.com/coder/websocket github.com/coder/websocket/wsjson
github.com/golang-jwt/jwt/v5 github.com/google/uuid github.com/imroc/req/v3
github.com/redis/go-redis/v9 github.com/tidwall/gjson github.com/tidwall/sjson go.uber.org/zap
golang.org/x/crypto/curve25519 golang.org/x/crypto/nacl/box golang.org/x/mod/semver
golang.org/x/net/html golang.org/x/sync/errgroup`, Tests: `github.com/stretchr/testify/assert github.com/stretchr/testify/require
github.com/stretchr/testify/suite`},
}

var moduleDependencies = map[string]dependencySet{
	"ent": {Production: `ent/... internal/account internal/apikey internal/billing internal/egress internal/identity
internal/promotion internal/protocol internal/routing internal/routing/accessview
internal/routing/capability internal/scheduler/policy internal/site`, Tests: ""},
	"internal/account": {Production: `ent/... internal/account/... internal/billing internal/egress internal/egress/httpapi/dto
internal/egress/provider internal/idempotency internal/idempotency/httpapi internal/identity/contact
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/redis/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/gemini internal/protocol/google internal/protocol/grok internal/protocol/openai
internal/routing internal/routing/accessview internal/routing/capability
internal/routing/httpapi/dto internal/routing/modelmap internal/scheduler internal/scheduler/policy
internal/server/httpx internal/settings internal/upstream internal/upstream/anthropic
internal/upstream/anthropic/oauth internal/upstream/antigravity internal/upstream/bedrock
internal/upstream/deepseek internal/upstream/gemini internal/upstream/gemini/codeassist
internal/upstream/grok internal/upstream/kimi internal/upstream/ollama internal/upstream/openai
internal/upstream/qoder internal/upstream/usagecontract internal/upstream/usageprovider
internal/upstream/usageview internal/upstream/vertex internal/upstream/zhipu internal/usage`, Tests: `internal/config internal/gateway internal/gateway/forward internal/gateway/media
internal/gateway/provider/modelidentity internal/gateway/requeststate internal/gateway/session
internal/idempotency/testkit internal/identity/httpapi/authctx internal/infra/timingwheel/...
internal/ops internal/routing/provider internal/testutil/assertion internal/testutil/rediscontainer`},
	"internal/apikey": {Production: `ent/... internal/apikey/... internal/billing internal/billing/postgres internal/billing/pricing
internal/config internal/idempotency/httpapi internal/identity internal/identity/contact
internal/identity/httpapi/authctx internal/infra/postgres/... internal/pkg/ internal/protocol
internal/routing internal/routing/accessview internal/routing/capability internal/routing/modelmap
internal/server/httpx internal/team internal/usage/postgres/query`, Tests: `internal/creative internal/routing/httpapi/dto internal/scheduler internal/testutil/rediscontainer
migrations`},
	"internal/audit": {Production: `internal/audit/... internal/identity/httpapi internal/identity/httpapi/authctx
internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/ internal/server/httpx
internal/settings`, Tests: ""},
	"internal/backup": {Production: `internal/backup/... internal/identity/httpapi/authctx internal/infra/telemetry/... internal/pkg/
internal/server/httpx`, Tests: "internal/identity internal/infra/postgres/..."},
	"internal/batchimage": {Production: `ent/... internal/account internal/account/provider internal/apikey internal/batchimage/...
internal/billing internal/gateway/httpapi internal/infra/postgres/... internal/infra/telemetry/...
internal/pkg/ internal/protocol internal/routing internal/routing/capability
internal/routing/modelmap internal/server/httpx internal/upstream internal/upstream/gemini
internal/upstream/vertex internal/usage`, Tests: `internal/billing/provider internal/billing/testkit internal/config internal/gateway/completion
internal/gateway/modeltrace internal/gateway/provider/modelidentity internal/testutil/assertion
internal/testutil/postgrescontainer internal/testutil/rediscontainer`},
	"internal/billing": {Production: `ent/... internal/billing/... internal/egress internal/gateway/provider
internal/gateway/provider/modelidentity internal/idempotency/httpapi internal/identity/contact
internal/identity/httpapi/authctx internal/infra/httpclient/... internal/infra/postgres/...
internal/infra/telemetry/... internal/notification/contract internal/pkg/ internal/protocol
internal/protocol/openai internal/routing internal/routing/capability internal/routing/testkit
internal/server/httpx internal/server/middleware internal/settings`, Tests: `internal/batchimage internal/batchimage/postgres internal/config internal/gateway/media
internal/testutil/rediscontainer internal/testutil/sqlite internal/upstream/grok
internal/upstream/openai`},
	"internal/config": {Production: `internal/config/... internal/identity/authconfig internal/scheduler/policy
internal/server/clientip/policy internal/server/httpconfig`, Tests: ""},
	"internal/creative": {Production: `ent/... internal/account internal/account/provider internal/billing internal/billing/pricing
internal/creative/... internal/identity/httpapi/authctx internal/infra/postgres/... internal/pkg/
internal/protocol internal/protocol/gemini internal/routing internal/routing/modelmap
internal/scheduler internal/server/httpx internal/settings internal/upstream
internal/upstream/gemini internal/upstream/grok internal/usage`, Tests: `internal/apikey internal/billing/provider internal/billing/testkit internal/config
internal/gateway/completion internal/gateway/media internal/gateway/provider/modelidentity
internal/identity internal/infra/telemetry/... internal/moderation internal/routing/capability
internal/testutil/assertion`},
	"internal/egress": {Production: `ent/... internal/account/transfer internal/egress/... internal/idempotency/httpapi
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/
internal/server/httpx`, Tests: "internal/billing"},
	"internal/gateway": {Production: `ent/... internal/account internal/account/provider internal/apikey internal/apikey/httpapi
internal/billing internal/billing/pricing internal/billing/testkit internal/creative
internal/creative/provider internal/egress internal/egress/provider internal/gateway/...
internal/identity internal/identity/httpapi/authctx internal/infra/httpclient/...
internal/infra/telemetry/... internal/infra/timingwheel/... internal/moderation internal/ops
internal/pkg/ internal/protocol internal/protocol/anthropic internal/protocol/bridge
internal/protocol/gemini internal/protocol/google internal/protocol/openai
internal/protocol/wirejson internal/routing internal/routing/accessview internal/routing/capability
internal/routing/modelmap internal/scheduler internal/scheduler/policy internal/scheduler/rediscache
internal/search internal/search/contract internal/server/clientip internal/server/httpx
internal/settings internal/upstream internal/upstream/anthropic internal/upstream/antigravity
internal/upstream/bedrock internal/upstream/gemini internal/upstream/gemini/codeassist
internal/upstream/grok internal/upstream/ollama internal/upstream/openai
internal/upstream/openai/liveattestation internal/upstream/openai/wsrelay internal/upstream/qoder
internal/upstream/vertex internal/usage`, Tests: `internal/billing/provider internal/config internal/moderation/provider internal/routing/testkit
internal/scheduler/rediscache/codec internal/search/provider internal/server/middleware
internal/settings/testkit internal/team internal/testutil/assertion internal/testutil/rediscontainer
internal/upstream/grok/testkit`},
	"internal/idempotency": {Production: `internal/idempotency/... internal/identity/httpapi/authctx internal/infra/postgres/...
internal/infra/telemetry/... internal/pkg/ internal/server/httpx`, Tests: "migrations"},
	"internal/identity": {Production: `ent/... internal/billing internal/billing/httpapi/dto internal/billing/postgres internal/config
internal/gateway/admission internal/idempotency/httpapi internal/identity/...
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/telemetry/...
internal/notification internal/notification/contract internal/pkg/ internal/promotion
internal/server/clientip internal/server/httpx internal/settings internal/site internal/team
internal/usage/postgres/query`, Tests: `internal/apikey internal/apikey/httpapi/dto internal/audit internal/gateway
internal/notification/smtp internal/routing internal/routing/capability internal/routing/httpapi/dto
internal/scheduler internal/testutil/rediscontainer migrations`},
	"internal/infra": {Production: "internal/infra/... internal/pkg/", Tests: "migrations"},
	"internal/moderation": {Production: `internal/infra/httpclient/... internal/infra/telemetry/... internal/moderation/...
internal/notification/contract internal/pkg/ internal/server/httpx internal/settings
internal/upstream`, Tests: `internal/egress internal/gateway/media internal/identity/postgres internal/notification
internal/upstream/openai`},
	"internal/notification": {Production: `internal/infra/telemetry/... internal/notification/... internal/pkg/ internal/server/httpx
internal/settings`, Tests: "internal/testutil/assertion"},
	"internal/ops": {Production: `internal/account internal/apikey internal/idempotency internal/idempotency/httpapi
internal/identity/httpapi/authctx internal/infra/httpclient/... internal/infra/postgres/...
internal/infra/telemetry/... internal/notification/contract internal/ops/... internal/pkg/
internal/scheduler internal/server/httpx internal/server/middleware internal/settings
internal/settings/preaggregation`, Tests: `ent/... internal/notification internal/notification/smtp internal/notification/testkit
internal/routing/capability internal/testutil/assertion migrations`},
	"internal/payment": {Production: `ent/... internal/billing internal/billing/httpapi internal/billing/postgres internal/identity
internal/identity/httpapi internal/identity/httpapi/authctx internal/payment/... internal/pkg/
internal/promotion internal/server/httpx internal/settings`, Tests: "internal/testutil/assertion internal/testutil/sqlite"},
	"internal/pkg": {Production: "internal/pkg/...", Tests: ""},
	"internal/promotion": {Production: `ent/... internal/identity internal/identity/contact internal/identity/httpapi/authctx
internal/identity/httpapi/dto internal/pkg/ internal/promotion/... internal/server/httpx
internal/settings`, Tests: ""},
	"internal/protocol": {Production: "internal/protocol/...", Tests: "internal/testutil/assertion"},
	"internal/routing": {Production: `ent/... internal/account internal/apikey/httpapi/dto internal/billing internal/billing/pricing
internal/billing/provider internal/idempotency internal/idempotency/httpapi
internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/openai internal/routing/... internal/scheduler/policy internal/server/httpx
internal/settings internal/upstream/anthropic internal/upstream/antigravity
internal/upstream/gemini/codeassist internal/upstream/grok internal/upstream/openai
internal/upstream/qoder`, Tests: `internal/account/provider internal/apikey internal/billing/testkit internal/gateway/media
internal/gateway/provider internal/gateway/provider/modelidentity internal/idempotency/testkit
internal/identity/httpapi/authctx internal/scheduler`},
	"internal/scheduler": {Production: `internal/account internal/egress internal/infra/postgres/... internal/infra/telemetry/...
internal/pkg/ internal/routing internal/routing/accessview internal/routing/capability
internal/scheduler/... internal/server/httpx internal/settings`, Tests: "internal/account/provider internal/billing internal/protocol internal/testutil/postgrescontainer"},
	"internal/search": {Production: `internal/infra/httpclient/... internal/pkg/ internal/search/... internal/server/httpx
internal/settings`, Tests: ""},
	"internal/server": {Production: `internal/apikey/httpapi internal/audit/httpapi internal/identity internal/identity/httpapi
internal/identity/httpapi/authctx internal/infra/telemetry/... internal/pkg/ internal/server/...
internal/settings`, Tests: `internal/account internal/account/httpapi internal/account/provider internal/apikey
internal/apikey/testkit internal/billing internal/billing/httpapi internal/billing/postgres
internal/config internal/egress internal/gateway internal/gateway/httpapi internal/gateway/provider
internal/gateway/provider/selection internal/gateway/requeststate internal/gateway/session
internal/gateway/testkit internal/gateway/tierpolicy internal/identity/testkit
internal/infra/httpclient/... internal/notification internal/ops internal/payment internal/promotion
internal/protocol internal/routing internal/routing/accessview internal/routing/capability
internal/routing/httpapi/dto internal/scheduler internal/settings/httpapi internal/settings/testkit
internal/site internal/team internal/upstream/anthropic internal/upstream/openai internal/usage
internal/usage/httpapi internal/usage/httpapi/ports`},
	"internal/settings": {Production: `ent/... internal/account internal/audit internal/billing internal/billing/httpapi internal/config
internal/creative internal/gateway internal/gateway/admission internal/gateway/clientmeta
internal/gateway/httpapi/dto internal/gateway/promptpolicy internal/gateway/testkit
internal/gateway/tierpolicy internal/identity internal/identity/authconfig internal/identity/contact
internal/identity/httpapi internal/identity/httpapi/authctx internal/identity/httpapi/dto
internal/identity/provider internal/moderation internal/notification internal/ops internal/payment
internal/pkg/ internal/promotion internal/routing internal/scheduler internal/scheduler/policy
internal/search internal/server/httpx internal/server/runtimeconfig internal/settings/...
internal/site internal/site/httpapi/dto internal/team internal/upstream/anthropic
internal/upstream/antigravity internal/upstream/grok internal/usage`, Tests: "internal/testutil/postgrescontainer"},
	"internal/setup": {Production: `internal/app/bootstrap internal/config internal/identity internal/infra/telemetry/...
internal/server/httpx internal/setup/...`, Tests: ""},
	"internal/site": {Production: `ent/... internal/identity/httpapi/authctx internal/infra/postgres/... internal/pkg/
internal/server/httpx internal/settings internal/site/...`, Tests: ""},
	"internal/team": {Production: `internal/billing internal/identity internal/identity/httpapi/authctx internal/pkg/
internal/server/httpx internal/settings internal/team/... internal/usage/postgres/query`, Tests: `internal/apikey internal/apikey/postgres internal/billing/postgres internal/notification
internal/notification/smtp internal/notification/testkit internal/site`},
	"internal/testutil": {Production: `ent/... internal/billing internal/gateway/rediscache internal/gateway/session
internal/infra/postgres/... internal/scheduler internal/testutil/... migrations`, Tests: ""},
	"internal/upstream": {Production: `internal/egress/urlpolicy internal/gateway/clientmeta internal/infra/httpclient/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/bridge internal/protocol/gemini internal/protocol/google internal/protocol/grok
internal/protocol/openai internal/protocol/wirejson internal/routing/capability
internal/routing/modelmap internal/upstream/...`, Tests: "internal/testutil/assertion internal/testutil/rediscontainer"},
	"internal/usage": {Production: `ent/... internal/account internal/apikey internal/apikey/httpapi/dto internal/apikey/postgres
internal/billing internal/billing/httpapi internal/billing/postgres internal/billing/pricing
internal/idempotency/httpapi internal/identity internal/identity/contact
internal/identity/httpapi/authctx internal/identity/httpapi/dto internal/identity/postgres
internal/infra/postgres/... internal/infra/telemetry/... internal/ops internal/pkg/ internal/routing
internal/routing/accessview internal/routing/httpapi/dto internal/routing/postgres
internal/server/httpx internal/settings internal/settings/preaggregation internal/team
internal/usage/...`, Tests: `internal/audit internal/audit/postgres internal/gateway/httpapi internal/infra/timingwheel/...
internal/routing/capability internal/testutil/assertion migrations`},
	"internal/web": {Production: "internal/server/middleware internal/web/...", Tests: ""},
	"migrations":   {Production: "migrations/...", Tests: ""},
}

var leafDependencies = map[string]dependencySet{
	"internal/account/httpapi/dto": {Production: `internal/account internal/account/httpapi/dto internal/egress/httpapi/dto internal/routing
internal/routing/httpapi/dto`, Tests: "internal/routing/capability"},
	"internal/account/transfer":   {Production: "internal/account/transfer internal/egress", Tests: ""},
	"internal/account/usageview":  {Production: "internal/account/usageview internal/upstream/usageview", Tests: ""},
	"internal/apikey/httpapi/dto": {Production: "internal/apikey internal/apikey/httpapi/dto internal/routing", Tests: "internal/billing"},
	"internal/app/bootstrap": {Production: `ent ent/securitysecret internal/app/bootstrap internal/config internal/identity
internal/identity/postgres internal/infra/crypto internal/infra/postgres internal/infra/redis
internal/infra/telemetry/logging internal/pkg/timezone internal/routing/postgres migrations`, Tests: "ent/enttest ent/group ent/runtime internal/billing internal/routing/capability"},
	"internal/app/lifecycle":       {Production: "internal/app/lifecycle", Tests: ""},
	"internal/billing/httpapi/dto": {Production: "internal/billing internal/billing/httpapi/dto", Tests: ""},
	"internal/billing/pricing":     {Production: "internal/billing/pricing internal/protocol internal/protocol/openai internal/routing/capability", Tests: ""},
	"internal/egress/httpapi/dto":  {Production: "internal/egress internal/egress/httpapi/dto", Tests: ""},
	"internal/egress/urlpolicy":    {Production: "internal/egress/urlpolicy internal/pkg/ipmatch", Tests: ""},
	"internal/gateway/clientmeta":  {Production: "internal/gateway/clientmeta internal/protocol/anthropic internal/protocol/openai", Tests: "internal/gateway/requeststate"},
	"internal/gateway/execution": {Production: `internal/account internal/apikey internal/billing internal/gateway/execution
internal/gateway/requeststate internal/routing internal/upstream`, Tests: ""},
	"internal/gateway/httpapi/dto":      {Production: "internal/gateway/httpapi/dto", Tests: ""},
	"internal/gateway/modeldisplay":     {Production: "internal/gateway/modeldisplay internal/routing/capability", Tests: ""},
	"internal/identity/authconfig":      {Production: "internal/identity/authconfig", Tests: ""},
	"internal/identity/contact":         {Production: "internal/identity/contact", Tests: ""},
	"internal/identity/httpapi/dto":     {Production: "internal/billing/httpapi/dto internal/identity internal/identity/httpapi/dto", Tests: "internal/billing"},
	"internal/infra/telemetry/logevent": {Production: "internal/infra/telemetry/logevent", Tests: ""},
	"internal/moderation/contract":      {Production: "internal/moderation/contract", Tests: ""},
	"internal/notification/contract":    {Production: "internal/notification/contract internal/pkg/apperror", Tests: ""},
	"internal/notification/httpapi/dto": {Production: "internal/notification/httpapi/dto", Tests: ""},
	"internal/pkg/apperror":             {Production: "internal/pkg/apperror", Tests: ""},
	"internal/pkg/ipmatch":              {Production: "internal/pkg/ipmatch", Tests: ""},
	"internal/pkg/logredact":            {Production: "internal/pkg/logredact", Tests: ""},
	"internal/pkg/oauthpkce":            {Production: "internal/pkg/oauthpkce", Tests: ""},
	"internal/pkg/pagination":           {Production: "internal/pkg/pagination", Tests: ""},
	"internal/pkg/querycache":           {Production: "internal/pkg/querycache", Tests: ""},
	"internal/pkg/timezone":             {Production: "internal/pkg/timezone", Tests: ""},
	"internal/protocol":                 {Production: "internal/protocol", Tests: ""},
	"internal/protocol/anthropic":       {Production: "internal/protocol internal/protocol/anthropic internal/protocol/wirejson", Tests: "internal/testutil/assertion"},
	"internal/protocol/bridge": {Production: `internal/protocol internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/openai internal/protocol/wirejson`, Tests: ""},
	"internal/protocol/gemini":    {Production: "internal/protocol/gemini", Tests: ""},
	"internal/protocol/google":    {Production: "internal/protocol/google", Tests: ""},
	"internal/protocol/grok":      {Production: "internal/protocol/grok", Tests: ""},
	"internal/protocol/openai":    {Production: "internal/protocol internal/protocol/openai internal/protocol/wirejson", Tests: ""},
	"internal/protocol/wirejson":  {Production: "internal/protocol/wirejson", Tests: ""},
	"internal/routing/accessview": {Production: "internal/billing/pricing internal/protocol internal/routing/accessview internal/scheduler/policy", Tests: ""},
	"internal/routing/capability": {Production: "internal/protocol internal/protocol/openai internal/routing/capability", Tests: ""},
	"internal/routing/httpapi/dto": {Production: `internal/billing/pricing internal/protocol internal/routing internal/routing/httpapi/dto
internal/scheduler/policy`, Tests: "internal/billing internal/routing/capability"},
	"internal/routing/modelmap":       {Production: "internal/routing/modelmap", Tests: ""},
	"internal/scheduler/policy":       {Production: "internal/scheduler/policy", Tests: ""},
	"internal/search/contract":        {Production: "internal/search/contract", Tests: ""},
	"internal/server/clientip/policy": {Production: "internal/server/clientip/policy", Tests: ""},
	"internal/server/httpapi/dto":     {Production: "internal/server/httpapi/dto", Tests: ""},
	"internal/server/httpconfig":      {Production: "internal/server/httpconfig", Tests: ""},
	"internal/settings/httpapi/dto": {Production: `internal/account internal/billing/httpapi internal/creative internal/gateway/httpapi/dto
internal/gateway/promptpolicy internal/identity internal/identity/httpapi/dto internal/ops
internal/payment internal/settings/httpapi/dto internal/site/httpapi/dto`, Tests: ""},
	"internal/site/httpapi/dto":       {Production: "internal/site/httpapi/dto", Tests: ""},
	"internal/upstream/usagecontract": {Production: "internal/upstream/usagecontract internal/upstream/usageview", Tests: ""},
	"internal/upstream/usageview":     {Production: "internal/pkg/apperror internal/upstream/usageview", Tests: ""},
	"internal/usage/httpapi/dto": {Production: `internal/apikey internal/apikey/httpapi/dto internal/billing internal/billing/httpapi
internal/identity internal/identity/httpapi/dto internal/ops internal/routing
internal/routing/httpapi/dto internal/usage internal/usage/httpapi/dto`, Tests: ""},
}

var platformDependencies = map[string]dependencySet{
	"internal/upstream/anthropic": {Production: `internal/gateway/clientmeta internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/
internal/protocol internal/protocol/anthropic internal/routing/capability internal/routing/modelmap
internal/upstream internal/upstream/anthropic/...`, Tests: "internal/testutil/rediscontainer"},
	"internal/upstream/antigravity": {Production: `internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/google internal/protocol/openai internal/upstream
internal/upstream/antigravity/...`, Tests: ""},
	"internal/upstream/bedrock": {Production: `internal/infra/telemetry/... internal/protocol internal/protocol/anthropic internal/upstream
internal/upstream/bedrock/...`, Tests: ""},
	"internal/upstream/deepseek": {Production: `internal/upstream/deepseek/... internal/upstream/internal/usageclient
internal/upstream/usagecontract internal/upstream/usageview`, Tests: ""},
	"internal/upstream/gemini": {Production: `internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/google internal/protocol/openai internal/upstream internal/upstream/gemini/...`, Tests: ""},
	"internal/upstream/grok": {Production: `internal/egress/urlpolicy internal/infra/httpclient/... internal/pkg/ internal/protocol
internal/protocol/bridge internal/protocol/grok internal/protocol/openai internal/protocol/wirejson
internal/upstream internal/upstream/grok/... internal/upstream/usageview`, Tests: ""},
	"internal/upstream/kimi": {Production: `internal/upstream/internal/usageclient internal/upstream/kimi/... internal/upstream/usagecontract
internal/upstream/usageview`, Tests: ""},
	"internal/upstream/ollama": {Production: "internal/protocol/openai internal/upstream/ollama/... internal/upstream/usageview", Tests: ""},
	"internal/upstream/openai": {Production: `internal/egress/urlpolicy internal/gateway/clientmeta internal/infra/httpclient/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/bridge internal/protocol/openai internal/protocol/wirejson internal/upstream
internal/upstream/openai/...`, Tests: "internal/testutil/assertion"},
	"internal/upstream/qoder": {Production: `internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/openai internal/upstream
internal/upstream/qoder/...`, Tests: ""},
	"internal/upstream/usageprovider": {Production: `internal/upstream/internal/usageclient internal/upstream/usagecontract
internal/upstream/usageprovider/... internal/upstream/usageview`, Tests: "internal/infra/httpclient/..."},
	"internal/upstream/vertex": {Production: `internal/infra/httpclient/... internal/protocol/anthropic internal/protocol/google
internal/upstream/internal/googleauth internal/upstream/vertex/...`, Tests: "internal/infra/telemetry/..."},
	"internal/upstream/zhipu": {Production: `internal/upstream/internal/usageclient internal/upstream/usagecontract internal/upstream/usageview
internal/upstream/zhipu/...`, Tests: ""},
}

// 纯包只使用明确的内存计算和编码库；不能通过通用标准库许可获得 I/O。
const pureStandard = `bufio bytes context crypto/rand crypto/sha256 encoding/base64 encoding/hex encoding/json errors fmt
golang.org/x/mod/semver golang.org/x/net/http/httpguts golang.org/x/sync/singleflight io iter maps
math net/textproto net/url reflect regexp slices sort strconv strings sync testing time unicode/utf8
unsafe`

var pureFileStandard = map[string]string{
	"internal/gateway/clientmeta/claude_detection_original_test.go": "net/http/httptest",
	"internal/gateway/clientmeta/claude_validator_original_test.go": "net/http net/http/httptest os",
	"internal/upstream/usagecontract/request.go":                    "net/http",
}

var ioFileExceptions = map[string]string{
	"internal/account/health_spark.go":                           "net/http",
	"internal/backup/backup_test.go":                             "database/sql",
	"internal/gateway/httpapi/live_moderation_fixture_test.go":   "database/sql",
	"internal/moderation/legacy_fixture_test.go":                 "database/sql",
	"internal/usage/httpapi/admin/usage_cleanup_handler_test.go": "database/sql",
}

// 窄权限属于指定文件，不能由相邻文件或目标子包继承。
var filePermissions = []filePermission{
	{Scope: "internal/account", Imports: "internal/account/provider", Files: `admin_editor_original_fixture_test.go admin_legacy_extra_original_test.go
admin_shadow_original_test.go`},
	{Scope: "internal/apikey", Imports: "internal/apikey/postgres", Files: "admin_group_original_test.go"},
	{Scope: "internal/apikey/postgres", Imports: "internal/billing/postgres internal/usage/postgres/query", Files: "key_store.go"},
	{Scope: "internal/backup", Imports: "internal/backup/provider internal/infra/postgres", Files: "backup_test.go"},
	{Scope: "internal/batchimage", Imports: "internal/batchimage/provider", Files: `cleanup_original_test.go download_original_test.go mvp_original_test.go pipeline_fixture_test.go
processor_original_test.go public_fixture_test.go public_original_test.go
result_usecase_fixture_test.go settlement_original_test.go`},
	{Scope: "internal/batchimage", Imports: "internal/billing/provider internal/gateway/provider/modelidentity", Files: "public_price_fixture_test.go"},
	{Scope: "internal/billing", Imports: "internal/billing/postgres", Files: `admin_redeem_mutations_original_test.go original_subscription_transaction_test.go
subscription_original_fixture_test.go`},
	{Scope: "internal/billing", Imports: "internal/billing/provider", Files: `calculator_calculator_fixture_test.go calculator_pricing_provider_fixture_test.go
calculator_pricing_stub_helpers_test.go original_model_pricing_resolver_catalog_alias_test.go
original_model_pricing_resolver_test.go`},
	{Scope: "internal/billing", Imports: "internal/gateway/provider/modelidentity", Files: `calculator_pricing_provider_fixture_test.go consumer_channel_time_pricing_billing_test.go
original_billing_service_test.go`},
	{Scope: "internal/billing/postgres", Imports: "internal/batchimage/postgres", Files: "repo_unit_test.go"},
	{Scope: "internal/creative", Imports: "internal/creative/provider", Files: "catalog_original_test.go creative_public_fixture_test.go original_creative_public_test.go"},
	{Scope: "internal/creative", Imports: "internal/gateway/provider/modelidentity", Files: "creative_public_fixture_test.go support_fixture_test.go"},
	{Scope: "internal/creative", Imports: "internal/billing/provider", Files: "support_fixture_test.go"},
	{Scope: "internal/gateway/clientmeta", Imports: "net/http/httptest", Files: "claude_detection_original_test.go claude_validator_original_test.go"},
	{Scope: "internal/gateway/clientmeta", Imports: "net/http os", Files: "claude_validator_original_test.go"},
	{Scope: "internal/gateway/completion", Imports: "internal/gateway/provider", Files: "original_gateway_record_usage_test.go original_openai_gateway_record_usage_test.go"},
	{Scope: "internal/gateway/completion", Imports: "internal/billing/provider", Files: `recording_calculator_fixture_test.go recording_pricing_provider_fixture_test.go
recording_pricing_stub_helpers_test.go`},
	{Scope: "internal/gateway/completion", Imports: "internal/gateway/provider/modelidentity", Files: "recording_pricing_provider_fixture_test.go"},
	{Scope: "internal/gateway/media", Imports: "internal/gateway/rediscache", Files: "video_integration_test.go"},
	{Scope: "internal/gateway/rediscache", Imports: "internal/scheduler/rediscache", Files: "session.go"},
	{Scope: "internal/identity", Imports: "internal/billing/postgres", Files: "admin_balance_original_test.go"},
	{Scope: "internal/identity", Imports: "internal/identity/postgres", Files: "admin_delete_user_original_test.go"},
	{Scope: "internal/identity", Imports: "internal/identity/provider", Files: "auth_settings_original_fixture_test.go dingtalk_original_test.go"},
	{Scope: "internal/identity/postgres", Imports: "internal/billing/postgres", Files: "auth_state.go user_repo.go"},
	{Scope: "internal/identity/postgres", Imports: "internal/usage/postgres/query", Files: "user_repo.go"},
	{Scope: "internal/moderation", Imports: "internal/moderation/provider", Files: "legacy_fixture_test.go"},
	{Scope: "internal/moderation/postgres", Imports: "internal/identity/postgres", Files: "fixture_test.go"},
	{Scope: "internal/payment/postgres", Imports: "internal/billing/postgres", Files: "consumer_order_snapshot_test.go"},
	{Scope: "internal/routing", Imports: "internal/gateway/provider", Files: "group_admin_original_fixture_test.go"},
	{Scope: "internal/routing", Imports: "internal/routing/provider", Files: `group_admin_original_fixture_test.go group_management_ports_original_test.go
marketplace_fixture_test.go`},
	{Scope: "internal/routing", Imports: "internal/billing/provider", Files: `group_admin_original_fixture_test.go marketplace_catalog_fixture_test.go marketplace_fixture_test.go`},
	{Scope: "internal/routing", Imports: "internal/account/provider", Files: "group_management_ports_original_test.go"},
	{Scope: "internal/routing", Imports: "internal/gateway/provider/modelidentity", Files: "marketplace_catalog_fixture_test.go marketplace_fixture_test.go marketplace_quote_fixture_test.go"},
	{Scope: "internal/scheduler/rediscache", Imports: "internal/account/provider", Files: "scheduler_cache_integration_test.go scheduler_cache_unit_test.go"},
	{Scope: "internal/search", Imports: "internal/search/provider", Files: "config_original_test.go"},
	{Scope: "internal/setup", Imports: "internal/app/bootstrap", Files: "setup.go"},
	{Scope: "internal/team/postgres", Imports: "internal/apikey/postgres internal/billing/postgres", Files: "invitation_preview_original_test.go"},
	{Scope: "internal/team/postgres", Imports: "internal/usage/postgres/query", Files: "team.go"},
	{Scope: "internal/upstream/usagecontract", Imports: "net/http", Files: "request.go"},
	{Scope: "internal/usage/postgres", Imports: "internal/audit/postgres", Files: "aggregation_audit_transactions_integration_test.go"},
	{Scope: "internal/usage/postgres", Imports: "internal/billing/postgres", Files: "aggregation_fixture_test.go support.go"},
	{Scope: "internal/usage/postgres", Imports: "internal/apikey/postgres internal/identity/postgres internal/routing/postgres", Files: "support.go"},
}

var permissionSubscopes = map[string]string{
	"internal/account": `internal/account/httpapi internal/account/postgres internal/account/provider
internal/account/rediscache internal/account/transfer internal/account/usageview`,
	"internal/apikey": "internal/apikey/httpapi internal/apikey/postgres internal/apikey/rediscache internal/apikey/testkit",
	"internal/backup": "internal/backup/httpapi internal/backup/provider",
	"internal/batchimage": `internal/batchimage/httpapi internal/batchimage/postgres internal/batchimage/provider
internal/batchimage/rediscache`,
	"internal/billing": `internal/billing/httpapi internal/billing/postgres internal/billing/pricing
internal/billing/provider internal/billing/rediscache internal/billing/testkit`,
	"internal/creative": `internal/creative/httpapi internal/creative/postgres internal/creative/provider
internal/creative/rediscache`,
	"internal/gateway/media": "internal/gateway/media/provider",
	"internal/identity": `internal/identity/authconfig internal/identity/contact internal/identity/httpapi
internal/identity/postgres internal/identity/provider internal/identity/rediscache
internal/identity/testkit`,
	"internal/moderation": `internal/moderation/contract internal/moderation/httpapi internal/moderation/postgres
internal/moderation/provider internal/moderation/rediscache`,
	"internal/routing": `internal/routing/accessview internal/routing/capability internal/routing/httpapi
internal/routing/modelmap internal/routing/postgres internal/routing/provider
internal/routing/testkit`,
	"internal/scheduler/rediscache": "internal/scheduler/rediscache/codec",
	"internal/search":               `internal/search/contract internal/search/httpapi internal/search/provider internal/search/rediscache`,
	"internal/usage/postgres":       "internal/usage/postgres/query",
}

var permissionChildImports = map[string]string{
	"internal/gateway/clientmeta": "net/http/httptest",
	"internal/gateway/completion": "internal/gateway/provider/modelidentity",
	"internal/routing":            "internal/gateway/provider/modelidentity",
}
