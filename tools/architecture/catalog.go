package architecture

// catalog.go declares supported package roles, internal module relationships,
// and narrow file permissions for architecture checks. Policy matching consumes
// these tables so a dependency selected for reviewed instrumentation cannot
// silently authorize neighboring files or unrelated adapters in another module.

// pureStandard permits in-memory parsing, encoding, Unicode validation, and caller-owned
// streams for pure packages. File and network acquisition require narrower permissions.
const pureStandard = `bufio bytes context crypto/rand crypto/sha256 encoding/base64 encoding/binary encoding/hex encoding/json errors fmt
golang.org/x/mod/semver golang.org/x/net/http/httpguts golang.org/x/sync/singleflight io iter maps
math net/textproto net/url reflect regexp slices sort strconv strings sync testing time unicode unicode/utf8
unsafe`

var (
	// The dependency tables list supported module collaboration and library choices.
	// Directory roles and common restrictions remain defined by policy.go.

	roleLibraries = map[string]dependencySet{
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
golang.org/x/image/webp golang.org/x/mod/semver golang.org/x/net/http/httpguts golang.org/x/net/publicsuffix
golang.org/x/sync/errgroup golang.org/x/sync/singleflight`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/redis/go-redis/v9 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/testcontainers/testcontainers-go/modules/redis`},
		"fixture": {Production: `entgo.io/ent/dialect entgo.io/ent/dialect/sql github.com/alicebob/miniredis/v2
github.com/gin-gonic/gin github.com/lib/pq github.com/redis/go-redis/v9
github.com/stretchr/testify/require github.com/stretchr/testify/suite
github.com/testcontainers/testcontainers-go github.com/testcontainers/testcontainers-go/wait
github.com/testcontainers/testcontainers-go/modules/postgres
github.com/testcontainers/testcontainers-go/modules/redis modernc.org/sqlite`, Tests: `github.com/DATA-DOG/go-sqlmock github.com/fxamacker/cbor/v2 github.com/golang-jwt/jwt/v5
github.com/google/uuid github.com/patrickmn/go-cache github.com/pquerna/otp/totp
github.com/stretchr/testify/assert github.com/stripe/stripe-go/v85`},
		"http": {Production: `github.com/modelcontextprotocol/go-sdk/mcp github.com/google/jsonschema-go/jsonschema github.com/coder/websocket github.com/gin-gonic/gin github.com/gin-gonic/gin/binding
github.com/google/uuid github.com/google/wire github.com/gorilla/websocket
github.com/klauspost/compress/zstd github.com/tidwall/gjson github.com/tidwall/sjson go.uber.org/zap
golang.org/x/net/http/httpguts golang.org/x/net/http2 golang.org/x/sync/singleflight`, Tests: `entgo.io/ent/dialect entgo.io/ent/dialect/sql github.com/modelcontextprotocol/go-sdk/auth github.com/modelcontextprotocol/go-sdk/oauthex golang.org/x/oauth2 github.com/alicebob/miniredis/v2 github.com/redis/go-redis/v9 github.com/cespare/xxhash/v2 github.com/imroc/req/v3 github.com/stretchr/testify/assert
github.com/stretchr/testify/require go.uber.org/zap/zaptest/observer`},
		"infra": {Production: `github.com/tidwall/gjson github.com/andybalholm/brotli github.com/imroc/req/v3 github.com/klauspost/compress/zstd
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
		"provider": {Production: `gopkg.in/yaml.v3
github.com/hashicorp/go-plugin github.com/hashicorp/go-plugin/runner github.com/hashicorp/go-hclog
google.golang.org/grpc/... google.golang.org/protobuf/... google.golang.org/genproto/googleapis/rpc/...
github.com/shirou/gopsutil/v4/process golang.org/x/time/rate
golang.org/x/crypto/bcrypt github.com/ory/fosite github.com/ory/fosite/compose
github.com/alibabacloud-go/captcha-20230305/client
github.com/alibabacloud-go/darabonba-openapi/v2/utils github.com/alibabacloud-go/tea/dara
github.com/alibabacloud-go/tea/tea github.com/aws/aws-sdk-go-v2/aws
github.com/aws/aws-sdk-go-v2/aws/signer/v4 github.com/aws/aws-sdk-go-v2/config
github.com/aws/aws-sdk-go-v2/credentials github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager
github.com/aws/aws-sdk-go-v2/service/s3 github.com/coder/websocket
github.com/go-webauthn/webauthn/protocol github.com/go-webauthn/webauthn/webauthn
github.com/golang-jwt/jwt/v5 github.com/google/uuid github.com/imroc/req/v3
github.com/redis/go-redis/v9 github.com/robfig/cron/v3 github.com/shirou/gopsutil/v4/cpu
github.com/shirou/gopsutil/v4/host github.com/shirou/gopsutil/v4/disk github.com/shirou/gopsutil/v4/mem github.com/shopspring/decimal
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
		"pure": {Production: `golang.org/x/text/language github.com/tidwall/gjson github.com/tidwall/sjson golang.org/x/mod/semver
golang.org/x/net/http/httpguts golang.org/x/sync/singleflight`, Tests: "github.com/stretchr/testify/assert github.com/stretchr/testify/require"},
		"redis": {Production: "github.com/redis/go-redis/v9", Tests: `github.com/alicebob/miniredis/v2 github.com/stretchr/testify/assert
github.com/stretchr/testify/require github.com/stretchr/testify/suite
github.com/testcontainers/testcontainers-go/modules/redis`},
		"schema": {Production: `entgo.io/ent entgo.io/ent/dialect entgo.io/ent/dialect/entsql entgo.io/ent/dialect/sql
entgo.io/ent/schema entgo.io/ent/schema/edge entgo.io/ent/schema/field entgo.io/ent/schema/index
entgo.io/ent/schema/mixin`, Tests: `entgo.io/ent/dialect/sql/schema entgo.io/ent/entc/load github.com/stretchr/testify/require
modernc.org/sqlite github.com/lib/pq github.com/testcontainers/testcontainers-go/modules/postgres`},
		"upstream": {Production: `github.com/aws/aws-sdk-go-v2/aws github.com/aws/aws-sdk-go-v2/aws/signer/v4
github.com/cespare/xxhash/v2 github.com/coder/websocket github.com/coder/websocket/wsjson
github.com/golang-jwt/jwt/v5 github.com/google/uuid github.com/imroc/req/v3
github.com/redis/go-redis/v9 github.com/tidwall/gjson github.com/tidwall/sjson go.uber.org/zap
golang.org/x/crypto/curve25519 golang.org/x/crypto/nacl/box golang.org/x/mod/semver
golang.org/x/net/html golang.org/x/sync/errgroup`, Tests: `github.com/stretchr/testify/assert github.com/stretchr/testify/require
github.com/stretchr/testify/suite`},
	}

	moduleDependencies = map[string]dependencySet{
		"internal/bstats": {Production: "internal/bstats/... internal/settings", Tests: ""},
		"internal/plugin": {Production: `internal/plugin/... internal/pkg/apperror internal/server/... internal/identity/httpapi/authctx ent/... plugin-sdk/go/...`, Tests: `internal/testutil/postgrescontainer internal/infra/crypto internal/identity migrations`},
		"internal/mcp": {Production: `internal/mcp/... internal/identity internal/identity/provider internal/identity/httpapi internal/identity/httpapi/authctx
internal/audit internal/server/adminapi internal/server/httpx`, Tests: `ent ent/runtime internal/identity/postgres internal/infra/crypto internal/testutil/postgrescontainer migrations`},
		"internal/requestlog": {Production: `internal/usage internal/requestlog/... internal/infra/telemetry/... internal/identity/httpapi/authctx internal/server/httpx`, Tests: `internal/testutil/postgrescontainer migrations`},
		"internal/modelcatalog": {Production: `internal/pkg/locale internal/modelcatalog internal/modelcatalog/provider internal/billing/pricing
internal/egress internal/infra/httpclient/... internal/infra/telemetry/...`, Tests: `internal/billing internal/billing/provider
internal/gateway/media internal/gateway/provider/modelidentity internal/routing/capability
internal/upstream/grok internal/upstream/openai`},
		"ent": {Production: `internal/pkg/locale ent/... internal/provider internal/apikey internal/billing internal/egress internal/identity
internal/promotion internal/protocol internal/routing internal/routing/accessview
internal/routing/capability internal/scheduler/policy internal/site`, Tests: ""},
		"internal/provider": {Production: `internal/server/adminapi internal/modelcatalog ent/... internal/provider/... internal/billing internal/egress internal/egress/httpapi/dto
internal/egress/provider internal/idempotency internal/idempotency/httpapi internal/identity/contact internal/identity/httpapi/authctx
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/redis/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/gemini internal/protocol/google internal/protocol/grok internal/protocol/openai internal/protocol/systemone
internal/routing internal/routing/accessview internal/routing/capability
internal/routing/httpapi/dto internal/routing/modelmap internal/scheduler internal/scheduler/policy
internal/server/httpx internal/settings internal/upstream internal/upstream/anthropic
internal/upstream/anthropic/oauth internal/upstream/antigravity internal/upstream/bedrock
internal/upstream/deepseek internal/upstream/gemini internal/upstream/gemini/codeassist
internal/upstream/grok internal/upstream/jev internal/upstream/kimi internal/upstream/ollama internal/upstream/opencode internal/upstream/openai
internal/upstream/qoder internal/upstream/usagecontract internal/upstream/usageprovider
internal/upstream/usageview internal/upstream/vertex internal/upstream/zhipu internal/usage`, Tests: `internal/modelcatalog/testkit internal/config internal/gateway internal/gateway/forward internal/gateway/media
internal/gateway/provider/modelidentity internal/gateway/requeststate internal/gateway/session
internal/idempotency/testkit internal/identity/httpapi/authctx internal/infra/timingwheel/...
internal/ops internal/routing/provider internal/testutil/assertion internal/testutil/rediscontainer internal/testutil/postgrescontainer`},
		"internal/apikey": {Production: `internal/server/adminapi ent/... internal/apikey/... internal/billing internal/billing/postgres internal/billing/pricing
internal/config internal/idempotency/httpapi internal/identity internal/identity/contact
internal/identity/httpapi/authctx internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/routing internal/routing/accessview internal/routing/capability internal/routing/modelmap
internal/server/httpx internal/team internal/usage/postgres/query`, Tests: `internal/creative internal/routing/httpapi/dto internal/scheduler internal/testutil/rediscontainer
migrations`},
		"internal/audit": {Production: `internal/server/adminapi internal/audit/... internal/identity/httpapi internal/identity/httpapi/authctx
internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/ internal/server/httpx
internal/settings`, Tests: ""},
		"internal/backup": {Production: `internal/server/adminapi internal/backup/... internal/identity/httpapi/authctx internal/infra/telemetry/... internal/pkg/
internal/server/httpx`, Tests: "internal/identity internal/infra/postgres/..."},
		"internal/batchimage": {Production: `internal/modelcatalog ent/... internal/provider internal/provider/provider internal/apikey internal/batchimage/...
internal/billing internal/gateway/httpapi internal/infra/postgres/... internal/infra/telemetry/...
internal/pkg/ internal/protocol internal/routing internal/routing/capability
internal/routing/modelmap internal/server/httpx internal/upstream internal/upstream/gemini
internal/upstream/vertex internal/usage`, Tests: `internal/modelcatalog/testkit internal/billing/pricing internal/billing/provider internal/billing/testkit internal/config internal/gateway/completion
internal/gateway/modeltrace internal/gateway/provider/modelidentity internal/testutil/assertion
internal/testutil/postgrescontainer internal/testutil/rediscontainer`},
		"internal/billing": {Production: `internal/server/adminapi internal/modelcatalog internal/modelcatalog/provider ent/... internal/billing/... internal/egress internal/gateway/provider
internal/gateway/provider/modelidentity internal/idempotency/httpapi internal/identity/contact
internal/identity/httpapi/authctx internal/infra/httpclient/... internal/infra/postgres/...
internal/infra/telemetry/... internal/notification/contract internal/pkg/ internal/protocol
internal/protocol/openai internal/routing internal/routing/capability internal/routing/testkit
internal/server/httpx internal/server/middleware internal/settings`, Tests: `internal/batchimage internal/batchimage/postgres internal/config internal/gateway/media
internal/testutil/rediscontainer internal/testutil/sqlite internal/upstream/grok
internal/upstream/openai`},
		"internal/config": {Production: `internal/config/... internal/identity/authconfig internal/scheduler/policy
internal/server/clientip/policy internal/server/httpconfig`, Tests: ""},
		"internal/creative": {Production: `internal/server/adminapi internal/modelcatalog ent/... internal/provider internal/provider/provider internal/billing internal/billing/pricing
internal/creative/... internal/identity/httpapi/authctx internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/
internal/protocol internal/protocol/gemini internal/routing internal/routing/capability internal/routing/modelmap
internal/scheduler internal/server/httpx internal/settings internal/upstream
internal/upstream/gemini internal/upstream/grok internal/upstream/openai internal/usage`, Tests: `internal/modelcatalog/testkit internal/apikey internal/billing/provider internal/billing/testkit internal/config
internal/gateway/completion internal/gateway/media internal/gateway/provider/modelidentity
internal/identity internal/infra/telemetry/... internal/moderation internal/routing/capability
internal/testutil/assertion`},
		"internal/egress": {Production: `internal/server/adminapi ent/... internal/provider/transfer internal/egress/... internal/idempotency/httpapi
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/
internal/server/httpx`, Tests: "internal/billing"},
		"internal/gateway": {Production: `internal/server/adminapi internal/modelcatalog internal/modelcatalog/testkit ent/... internal/provider internal/provider/provider internal/apikey internal/apikey/httpapi
internal/billing internal/billing/pricing internal/billing/testkit internal/creative
internal/creative/provider internal/egress internal/egress/provider internal/gateway/...
internal/identity internal/identity/httpapi/authctx internal/infra/httpclient/...
internal/infra/telemetry/... internal/infra/timingwheel/... internal/moderation internal/ops
internal/pkg/ internal/protocol internal/protocol/anthropic internal/protocol/bridge
internal/protocol/gemini internal/protocol/google internal/protocol/openai internal/protocol/systemone
internal/protocol/wirejson internal/routing internal/routing/accessview internal/routing/capability
internal/routing/modelmap internal/scheduler internal/scheduler/policy internal/scheduler/rediscache
internal/search internal/search/contract internal/server/clientip internal/server/httpx
internal/settings internal/upstream internal/upstream/anthropic internal/upstream/antigravity
internal/upstream/bedrock internal/upstream/gemini internal/upstream/gemini/codeassist
internal/upstream/grok internal/upstream/jev internal/upstream/ollama internal/upstream/opencode internal/upstream/openai
internal/upstream/openai/liveattestation internal/upstream/openai/ws/... internal/upstream/qoder
internal/upstream/vertex internal/usage`, Tests: `internal/modelcatalog/testkit internal/billing/provider internal/config internal/moderation/provider internal/routing/testkit
internal/scheduler/rediscache/codec internal/search/provider internal/server/middleware
internal/settings/testkit internal/team internal/testutil/assertion internal/testutil/rediscontainer
internal/upstream/grok/testkit internal/modelcatalog/provider`},
		"internal/idempotency": {Production: `internal/idempotency/... internal/identity/httpapi/authctx internal/infra/postgres/...
internal/infra/telemetry/... internal/pkg/ internal/server/httpx`, Tests: "migrations"},
		"internal/identity": {Production: `internal/server/adminapi ent/... internal/billing internal/billing/httpapi/dto internal/billing/postgres internal/config
internal/gateway/admission internal/idempotency/httpapi internal/identity/...
internal/infra/httpclient/... internal/infra/postgres/... internal/infra/telemetry/...
internal/notification internal/notification/contract internal/pkg/ internal/promotion
internal/server/clientip internal/server/httpx internal/settings internal/site internal/team
internal/usage/postgres/query`, Tests: `internal/infra/crypto internal/apikey internal/apikey/httpapi/dto internal/audit internal/gateway
internal/notification/smtp internal/routing internal/routing/capability internal/routing/httpapi/dto
internal/scheduler internal/testutil/rediscontainer internal/testutil/postgrescontainer migrations`},
		"internal/infra": {Production: "internal/infra/... internal/pkg/", Tests: "internal/testutil/rediscontainer migrations"},
		"internal/moderation": {Production: `internal/server/adminapi internal/infra/httpclient/... internal/infra/telemetry/... internal/moderation/...
internal/notification/contract internal/pkg/ internal/server/httpx internal/settings
internal/upstream`, Tests: `internal/egress internal/gateway/media internal/identity/postgres internal/notification
internal/upstream/openai`},
		"internal/notification": {Production: `internal/server/adminapi internal/infra/telemetry/... internal/notification/... internal/pkg/ internal/server/httpx
internal/settings`, Tests: "internal/testutil/assertion"},
		"internal/ops": {Production: `internal/server/adminapi internal/provider internal/apikey internal/idempotency internal/idempotency/httpapi
internal/identity/httpapi/authctx internal/infra/httpclient/... internal/infra/postgres/...
internal/infra/telemetry/... internal/notification/contract internal/ops/... internal/pkg/
internal/scheduler internal/server/httpx internal/server/middleware internal/settings
internal/settings/preaggregation`, Tests: `internal/testutil/rediscontainer ent/... internal/notification internal/notification/smtp internal/notification/testkit
internal/routing/capability internal/testutil/assertion migrations`},
		"internal/payment": {Production: `internal/server/adminapi ent/... internal/billing internal/billing/httpapi internal/billing/postgres internal/identity
internal/identity/httpapi internal/identity/httpapi/authctx internal/payment/... internal/pkg/
internal/promotion internal/server/httpx internal/settings`, Tests: "internal/testutil/assertion internal/testutil/sqlite"},
		"internal/pkg": {Production: "internal/pkg/...", Tests: ""},
		"internal/promotion": {Production: `internal/server/adminapi ent/... internal/identity internal/identity/contact internal/identity/httpapi/authctx
internal/identity/httpapi/dto internal/pkg/ internal/promotion/... internal/server/httpx
internal/settings`, Tests: ""},
		"internal/protocol": {Production: "internal/protocol/...", Tests: "internal/testutil/assertion"},
		"internal/routing": {Production: `internal/identity/httpapi/authctx internal/server/adminapi internal/modelcatalog ent/... internal/provider internal/apikey/httpapi/dto internal/billing internal/billing/pricing
internal/billing/provider internal/idempotency internal/idempotency/httpapi
internal/infra/postgres/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/openai internal/routing/... internal/scheduler/policy internal/server/httpx
internal/settings internal/upstream/anthropic internal/upstream/antigravity
internal/upstream/deepseek internal/upstream/kimi internal/upstream/zhipu
internal/upstream/gemini/codeassist internal/upstream/grok internal/upstream/openai
internal/upstream/qoder`, Tests: `internal/modelcatalog/testkit internal/provider/provider internal/apikey internal/billing/testkit internal/gateway/media
internal/gateway/provider internal/gateway/provider/modelidentity internal/idempotency/testkit
internal/identity/httpapi/authctx internal/scheduler internal/testutil/postgrescontainer migrations internal/modelcatalog/provider`},
		"internal/scheduler": {Production: `internal/server/adminapi internal/provider internal/egress internal/infra/postgres/... internal/infra/telemetry/...
internal/pkg/ internal/routing internal/routing/accessview internal/routing/capability
internal/scheduler/... internal/server/httpx internal/settings`, Tests: "internal/testutil/rediscontainer internal/provider/provider internal/billing internal/protocol internal/testutil/postgrescontainer"},
		"internal/search": {Production: `internal/server/adminapi internal/infra/httpclient/... internal/pkg/ internal/search/... internal/server/httpx
internal/settings`, Tests: "internal/testutil/rediscontainer "},
		"internal/server": {Production: `internal/apikey/httpapi internal/audit/httpapi internal/identity internal/identity/httpapi
internal/identity/httpapi/authctx internal/infra/telemetry/... internal/pkg/ internal/server/...
internal/settings`, Tests: `internal/provider internal/provider/httpapi internal/provider/provider internal/apikey
internal/apikey/testkit internal/billing internal/billing/httpapi internal/billing/postgres
internal/config internal/egress internal/gateway internal/gateway/httpapi internal/gateway/provider
internal/gateway/provider/selection internal/gateway/requeststate internal/gateway/session
internal/gateway/testkit internal/gateway/tierpolicy internal/identity/testkit
internal/infra/httpclient/... internal/notification internal/ops internal/payment internal/promotion
internal/protocol internal/protocol/systemone internal/routing internal/routing/accessview internal/routing/capability
internal/routing/httpapi/dto internal/scheduler internal/settings/httpapi internal/settings/testkit
internal/site internal/team internal/upstream/anthropic internal/upstream/openai internal/usage
internal/usage/httpapi internal/usage/httpapi/ports`},
		"internal/settings": {Production: `internal/bstats internal/server/adminapi internal/gateway/ws ent/... internal/provider internal/audit internal/billing internal/billing/httpapi internal/config
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
		"internal/site": {Production: `internal/server/adminapi ent/... internal/identity/httpapi/authctx internal/infra/postgres/... internal/pkg/
internal/server/httpx internal/settings internal/site/...`, Tests: "internal/testutil/postgrescontainer"},
		"internal/team": {Production: `internal/server/adminapi internal/billing internal/identity internal/identity/httpapi/authctx internal/pkg/
internal/server/httpx internal/settings internal/team/... internal/usage/postgres/query`, Tests: `internal/apikey internal/apikey/postgres internal/billing/postgres internal/notification
internal/notification/smtp internal/notification/testkit internal/site`},
		"internal/testutil": {Production: `ent/... internal/billing internal/gateway/rediscache internal/gateway/session
internal/infra/postgres/... internal/scheduler internal/testutil/... migrations`, Tests: ""},
		"internal/upstream": {Production: `internal/modelcatalog internal/egress/urlpolicy internal/gateway/clientmeta internal/infra/httpclient/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/bridge internal/protocol/gemini internal/protocol/google internal/protocol/grok
internal/protocol/openai internal/protocol/systemone internal/protocol/wirejson internal/routing/capability
internal/routing/modelmap internal/upstream/...`, Tests: "internal/modelcatalog/testkit internal/testutil/assertion internal/testutil/rediscontainer"},
		"internal/usage": {Production: `internal/server/adminapi ent/... internal/provider internal/apikey internal/apikey/httpapi/dto internal/apikey/postgres
internal/billing internal/billing/httpapi internal/billing/postgres internal/billing/pricing
internal/idempotency/httpapi internal/identity internal/identity/contact
internal/identity/httpapi/authctx internal/identity/httpapi/dto internal/identity/postgres
internal/infra/postgres/... internal/infra/telemetry/... internal/ops internal/pkg/ internal/routing
internal/routing/accessview internal/routing/httpapi/dto internal/routing/postgres
internal/server/httpx internal/settings internal/settings/preaggregation internal/team
internal/usage/...`, Tests: `internal/testutil/rediscontainer internal/ops/postgres internal/audit internal/audit/postgres internal/gateway/httpapi internal/infra/timingwheel/...
internal/routing/capability internal/testutil/assertion migrations`},
		"internal/web": {Production: "internal/pkg/locale internal/server/middleware internal/web/...", Tests: ""},
		"migrations":   {Production: "migrations/...", Tests: "internal/infra/postgres internal/pkg/locale"},
	}

	leafDependencies = map[string]dependencySet{
		"internal/site/content": {Production: "internal/site/content internal/pkg/locale", Tests: ""},
		"internal/pkg/locale":   {Production: "internal/pkg/locale internal/pkg/apperror", Tests: ""},
		"internal/provider/httpapi/dto": {Production: `internal/provider internal/provider/httpapi/dto internal/egress/httpapi/dto internal/routing
internal/routing/httpapi/dto`, Tests: "internal/routing/capability"},
		"internal/provider/transfer":  {Production: "internal/provider/transfer internal/egress", Tests: ""},
		"internal/provider/usageview": {Production: "internal/provider/usageview internal/upstream/usageview", Tests: ""},
		"internal/apikey/httpapi/dto": {Production: "internal/apikey internal/apikey/httpapi/dto internal/routing", Tests: "internal/billing"},
		"internal/app/bootstrap": {Production: `ent ent/securitysecret internal/app/bootstrap internal/config internal/identity
internal/identity/postgres internal/infra/crypto internal/infra/postgres internal/infra/redis
internal/infra/telemetry/logging internal/pkg/timezone internal/routing/postgres migrations`, Tests: "ent/enttest ent/group ent/runtime internal/billing internal/routing/capability"},
		"internal/app/lifecycle":       {Production: "internal/app/lifecycle", Tests: ""},
		"internal/billing/httpapi/dto": {Production: "internal/billing internal/billing/httpapi/dto", Tests: ""},
		"internal/billing/pricing":     {Production: "internal/modelcatalog internal/billing/pricing internal/protocol internal/protocol/openai internal/routing/capability", Tests: ""},
		"internal/egress/httpapi/dto":  {Production: "internal/egress internal/egress/httpapi/dto", Tests: ""},
		"internal/egress/urlpolicy":    {Production: "internal/egress/urlpolicy internal/pkg/ipmatch", Tests: ""},
		"internal/gateway/clientmeta":  {Production: "internal/gateway/clientmeta internal/protocol/anthropic internal/protocol/openai", Tests: "internal/gateway/requeststate"},
		"internal/gateway/execution": {Production: `internal/provider internal/apikey internal/billing internal/gateway/execution
internal/gateway/requeststate internal/routing internal/upstream`, Tests: ""},
		"internal/gateway/httpapi/dto":      {Production: "internal/pkg/locale internal/gateway/httpapi/dto", Tests: ""},
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
		"internal/pkg/requestcontext":       {Production: "internal/pkg/requestcontext", Tests: ""},
		"internal/pkg/timezone":             {Production: "internal/pkg/timezone", Tests: ""},
		"internal/protocol":                 {Production: "internal/protocol", Tests: ""},
		"internal/protocol/anthropic":       {Production: "internal/protocol internal/protocol/anthropic internal/protocol/wirejson", Tests: "internal/testutil/assertion"},
		"internal/protocol/bridge": {Production: `internal/protocol internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/openai internal/protocol/wirejson`, Tests: ""},
		"internal/protocol/systemone": {Production: "internal/protocol internal/protocol/systemone", Tests: ""},
		"internal/protocol/gemini":    {Production: "internal/protocol/gemini", Tests: ""},
		"internal/protocol/google":    {Production: "internal/protocol/google", Tests: ""},
		"internal/protocol/grok":      {Production: "internal/protocol/grok", Tests: ""},
		"internal/protocol/openai":    {Production: "internal/protocol internal/protocol/openai internal/protocol/wirejson", Tests: ""},
		"internal/protocol/wirejson":  {Production: "internal/protocol/wirejson", Tests: ""},
		"internal/routing/accessview": {Production: "internal/pkg/locale internal/billing/pricing internal/protocol internal/routing/accessview internal/scheduler/policy", Tests: ""},
		"internal/routing/capability": {Production: "internal/protocol internal/protocol/openai internal/routing/capability", Tests: ""},
		"internal/routing/httpapi/dto": {Production: `internal/pkg/locale internal/modelcatalog internal/billing/pricing internal/protocol internal/routing internal/routing/httpapi/dto
internal/scheduler/policy`, Tests: "internal/billing internal/routing/capability"},
		"internal/routing/modelmap":       {Production: "internal/routing/modelmap", Tests: ""},
		"internal/scheduler/policy":       {Production: "internal/scheduler/policy", Tests: ""},
		"internal/search/contract":        {Production: "internal/search/contract", Tests: ""},
		"internal/server/clientip/policy": {Production: "internal/server/clientip/policy", Tests: ""},
		"internal/server/httpapi/dto":     {Production: "internal/server/httpapi/dto", Tests: ""},
		"internal/server/httpconfig":      {Production: "internal/server/httpconfig", Tests: ""},
		"internal/settings/httpapi/dto": {Production: `internal/gateway/ws internal/pkg/locale internal/provider internal/billing/httpapi internal/creative internal/gateway/httpapi/dto
internal/gateway/promptpolicy internal/identity internal/identity/httpapi/dto internal/ops
internal/payment internal/settings/httpapi/dto internal/site/httpapi/dto`, Tests: ""},
		"internal/site/httpapi/dto":       {Production: "internal/pkg/locale internal/site/content internal/site/httpapi/dto", Tests: ""},
		"internal/upstream/usagecontract": {Production: "internal/upstream/usagecontract internal/upstream/usageview", Tests: ""},
		"internal/upstream/usageview":     {Production: "internal/pkg/apperror internal/upstream/usageview", Tests: ""},
		"internal/usage/httpapi/dto": {Production: `internal/apikey internal/apikey/httpapi/dto internal/billing internal/billing/httpapi
internal/identity internal/identity/httpapi/dto internal/ops internal/routing
internal/routing/httpapi/dto internal/usage internal/usage/httpapi/dto`, Tests: ""},
	}

	platformDependencies = map[string]dependencySet{
		"internal/upstream/opencode": {Production: "internal/upstream/opencode/... internal/upstream/internal/usageclient internal/upstream/usagecontract internal/upstream/usageview", Tests: ""},
		"internal/upstream/jev":      {Production: "internal/infra/httpclient/... internal/protocol internal/protocol/systemone internal/upstream internal/upstream/jev/...", Tests: ""},
		"internal/upstream/anthropic": {Production: `internal/gateway/clientmeta internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/
internal/protocol internal/protocol/anthropic internal/routing/capability internal/routing/modelmap
internal/upstream internal/upstream/anthropic/...`, Tests: "internal/testutil/rediscontainer"},
		"internal/upstream/antigravity": {Production: `internal/modelcatalog internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/google internal/protocol/openai internal/upstream
internal/upstream/antigravity/...`, Tests: ""},
		"internal/upstream/bedrock": {Production: `internal/modelcatalog internal/infra/telemetry/... internal/protocol internal/protocol/anthropic internal/upstream
internal/upstream/bedrock/...`, Tests: ""},
		"internal/upstream/deepseek": {Production: `internal/upstream/deepseek/... internal/upstream/internal/usageclient
internal/upstream/usagecontract internal/upstream/usageview`, Tests: ""},
		"internal/upstream/gemini": {Production: `internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/gemini
internal/protocol/google internal/protocol/openai internal/upstream internal/upstream/gemini/...`, Tests: ""},
		"internal/upstream/grok": {Production: `internal/modelcatalog internal/egress/urlpolicy internal/infra/httpclient/... internal/pkg/ internal/protocol
internal/protocol/bridge internal/protocol/grok internal/protocol/openai internal/protocol/wirejson
internal/upstream internal/upstream/grok/... internal/upstream/usageview`, Tests: "internal/modelcatalog/testkit"},
		"internal/upstream/kimi": {Production: `internal/upstream/internal/usageclient internal/upstream/kimi/... internal/upstream/usagecontract
internal/upstream/usageview`, Tests: ""},
		"internal/upstream/ollama": {Production: "internal/modelcatalog internal/protocol/openai internal/upstream/ollama/... internal/upstream/usageview", Tests: ""},
		"internal/upstream/openai": {Production: `internal/modelcatalog internal/egress/urlpolicy internal/gateway/clientmeta internal/infra/httpclient/...
internal/infra/telemetry/... internal/pkg/ internal/protocol internal/protocol/anthropic
internal/protocol/bridge internal/protocol/openai internal/protocol/wirejson internal/upstream
internal/upstream/openai/...`, Tests: "internal/testutil/assertion"},
		"internal/upstream/qoder": {Production: `internal/modelcatalog internal/infra/httpclient/... internal/infra/telemetry/... internal/pkg/ internal/protocol
internal/protocol/anthropic internal/protocol/bridge internal/protocol/openai internal/upstream
internal/upstream/qoder/...`, Tests: ""},
		"internal/upstream/usageprovider": {Production: `internal/upstream/internal/usageclient internal/upstream/usagecontract
internal/upstream/usageprovider/... internal/upstream/usageview`, Tests: "internal/infra/httpclient/..."},
		"internal/upstream/vertex": {Production: `internal/infra/httpclient/... internal/protocol/anthropic internal/protocol/google
internal/upstream/internal/googleauth internal/upstream/vertex/...`, Tests: "internal/infra/telemetry/..."},
		"internal/upstream/zhipu": {Production: `internal/upstream/internal/usageclient internal/upstream/usagecontract internal/upstream/usageview
internal/upstream/zhipu/...`, Tests: ""},
	}

	pureFileStandard = map[string]string{
		// SystemOne parses buffered multipart envelopes without acquiring files or
		// network connections; only the codec and its fixture builder need MIME helpers.
		"internal/protocol/systemone/payload.go":      "mime mime/multipart",
		"internal/protocol/systemone/payload_test.go": "mime/multipart",
		// Deterministic reference fixtures and allocation regression checks need
		// random sampling and runtime counters in this test file only.
		"internal/protocol/systemone/references_test.go": "math/rand/v2 runtime",
		"internal/pkg/locale/locale.go":                  "embed golang.org/x/text/language",
		"internal/pkg/locale/errors.go":                  "embed unicode",
		// 请求取消测试使用虚拟时钟检查截止时间和异步回调。
		"internal/pkg/requestcontext/context_test.go":          "testing/synctest",
		"internal/gateway/clientmeta/claude_validator_test.go": "net/http net/http/httptest os",
		"internal/upstream/usagecontract/request.go":           "net/http",
	}

	ioFileExceptions = map[string]string{
		"internal/mcp/httpapi/oauth_integration_test.go":     "database/sql",
		"internal/provider/health_spark.go":                  "net/http",
		"internal/backup/backup_external_test.go":            "database/sql",
		"internal/gateway/httpapi/live_test.go":              "database/sql",
		"internal/moderation/helpers_test.go":                "database/sql",
		"internal/usage/httpapi/admin/usage_handler_test.go": "database/sql",
	}

	// File permissions bind imports to reviewed source files; neighboring files
	// and target subpackages do not inherit these permissions.
	filePermissions = []filePermission{
		// Personal marketplace requests read the JWT subject through the shared identity context.
		{Scope: "internal/routing/httpapi", Imports: "internal/identity/httpapi/authctx", Files: "marketplace.go marketplace_test.go group_handler_test.go"},
		// The composite-key scenario reads the actual SystemOne envelope after
		// middleware rewriting, keeping its assertions aligned with the protocol parser.
		{Scope: "internal/server", Imports: "internal/protocol/systemone", Files: "middleware/api_key_model_scenario_test.go"},
		// OAuth registration hashes confidential-client secrets; revocation tests verify their authentication behavior.
		{Scope: "internal/identity/provider", Imports: "golang.org/x/crypto/bcrypt", Files: "mcp_oauth_clients.go mcp_oauth_test.go"},
		// Protocol acceptance uses migrated PostgreSQL and the production account reader on one connection pool.
		{Scope: "internal/mcp/httpapi", Imports: "database/sql ent ent/runtime entgo.io/ent/dialect entgo.io/ent/dialect/sql github.com/modelcontextprotocol/go-sdk/auth github.com/modelcontextprotocol/go-sdk/oauthex internal/identity/postgres golang.org/x/oauth2", Files: "oauth_integration_test.go"},
		// Creative's OpenAI adapter uses the shared native Images reader for mode
		// conversion; adjacent creative files retain their existing dependency limits.
		{Scope: "internal/creative", Imports: "internal/upstream/openai", Files: "provider/openai.go"},
		{Scope: "internal/gateway", Imports: "internal/modelcatalog/testkit", Files: "httpapi/grok_media_execution_test.go httpapi/helpers_test.go provider/creative_catalog_test.go provider/googleforward/gemini_attempts_external_test.go provider/googleforward/gemini_openai_external_test.go provider/googleforward/helpers_external_test.go provider/grok_codec_test.go provider/model_attributes_test.go provider/model_qualification_test.go provider/selection/candidate_policy_test.go provider/selection/probes_test.go testkit/runtime_settings.go ws/httpapi/openai_ws_http_bridge_test.go"},
		// Timing observers and response-body decoding tests inspect protocol events through these permitted files.
		{Scope: "internal/infra", Imports: "github.com/tidwall/gjson", Files: "telemetry/timing/events.go telemetry/timing/performance.go httpclient/response_body_test.go"},
		// Catalogue readers and offline capability fixtures receive permissions only
		// in the production or test files that consume them.
		{Scope: "internal/gateway/provider", Imports: "internal/modelcatalog", Files: "selection/candidate_policy_test.go model_qualification_test.go model_display.go model_attributes.go model_catalogue.go creative_catalog.go execution_provider.go qoder_runtime.go responses_effort.go grok_codec_test.go model_attributes_test.go googleforward/antigravity_openai.go googleforward/model_attributes.go googleforward/gemini_attempts_external_test.go googleforward/gemini_openai_external_test.go grokforward/contracts.go grokforward/responses.go openaiforward/transform_ports.go openaiforward/passthrough_ports.go messageforward/bedrock.go modelidentity/thinking.go"},
		{Scope: "internal/gateway/provider", Imports: "internal/modelcatalog/testkit", Files: "grok_codec_test.go model_attributes_test.go creative_catalog_test.go model_qualification_test.go selection/candidate_policy_test.go selection/probes_test.go googleforward/gemini_openai_external_test.go googleforward/gemini_attempts_external_test.go googleforward/helpers_external_test.go"},
		{Scope: "internal/provider/httpapi", Imports: "internal/modelcatalog/testkit", Files: "management_catalog_test.go provider_probe_scenario_test.go"},
		// Token persistence tests round-trip real ciphertext through the migrated store and identity service.
		{Scope: "internal/identity/postgres", Imports: "internal/infra/crypto", Files: "mcp_tokens_integration_test.go"},
		// 容器启动等待由共用测试入口提供，许可限定到接入该入口的文件。
		{Scope: "internal/infra/redis", Imports: "internal/testutil/rediscontainer", Files: "helpers_integration_test.go"},
		{Scope: "internal/ops/postgres", Imports: "internal/testutil/rediscontainer", Files: "main_integration_test.go"},
		{Scope: "internal/ops/rediscache", Imports: "internal/testutil/rediscontainer", Files: "main_integration_test.go"},
		{Scope: "internal/scheduler/rediscache", Imports: "internal/testutil/rediscontainer", Files: "main_integration_test.go"},
		{Scope: "internal/search/provider", Imports: "internal/testutil/rediscontainer", Files: "quota_reservation_scenario_integration_test.go"},
		{Scope: "internal/usage/postgres", Imports: "internal/testutil/rediscontainer", Files: "main_integration_test.go"},
		{Scope: "internal/usage/rediscache", Imports: "internal/testutil/rediscontainer", Files: "dashboard_integration_test.go"},
		{Scope: "internal/testutil/rediscontainer", Imports: "github.com/testcontainers/testcontainers-go github.com/testcontainers/testcontainers-go/wait", Files: "run.go"},
		{Scope: "migrations", Imports: "internal/infra/postgres github.com/lib/pq github.com/testcontainers/testcontainers-go/modules/postgres", Files: "platform_independent_groups_integration_test.go platform_independent_pricing_integration_test.go provider_names_integration_test.go product_brand_integration_test.go antigravity_retirement_integration_test.go user_localization_integration_test.go responses_ws_integration_test.go request_lookup_integration_test.go provider_balance_alert_integration_test.go usage_performance_integration_test.go custom_video_resolution_integration_test.go system_model_usage_integration_test.go"},
		{Scope: "internal/usage/postgres", Imports: "internal/ops/postgres", Files: "platform_snapshot_scenario_integration_test.go"},
		{Scope: "internal/provider", Imports: "internal/provider/provider", Files: "admin_edit_external_test.go helpers_external_test.go shadow_external_test.go"},
		{Scope: "internal/apikey", Imports: "internal/apikey/postgres", Files: "admin_external_test.go"},
		{Scope: "internal/apikey/postgres", Imports: "internal/billing/postgres internal/usage/postgres/query", Files: "key_store.go"},
		{Scope: "internal/backup", Imports: "internal/backup/provider internal/infra/postgres", Files: "backup_external_test.go"},
		{Scope: "internal/batchimage", Imports: "internal/batchimage/provider", Files: "cleanup_test.go helpers_test.go job_lifecycle_scenario_test.go processor_test.go public_test.go settlement_test.go"},
		{Scope: "internal/batchimage", Imports: "internal/billing/provider", Files: "public_test.go"},
		{Scope: "internal/billing", Imports: "internal/billing/postgres", Files: "redeem_admin_external_test.go subscription_service_test.go"},
		{Scope: "internal/billing", Imports: "internal/billing/provider", Files: "price_resolver_test.go"},
		{Scope: "internal/billing", Imports: "internal/modelcatalog/provider", Files: "calculator_external_test.go helpers_external_test.go price_resolver_test.go"},
		{Scope: "internal/billing", Imports: "internal/gateway/provider/modelidentity", Files: "calculator_external_test.go helpers_external_test.go"},
		{Scope: "internal/billing/postgres", Imports: "internal/batchimage/postgres", Files: "settlement_test.go"},
		{Scope: "internal/creative", Imports: "internal/creative/provider", Files: "helpers_external_test.go public_external_test.go"},
		{Scope: "internal/creative", Imports: "internal/gateway/provider/modelidentity", Files: "helpers_external_test.go public_external_test.go"},
		{Scope: "internal/creative", Imports: "internal/billing/provider", Files: "public_external_test.go"},
		{Scope: "internal/gateway/clientmeta", Imports: "net/http/httptest", Files: "claude_validator_test.go"},
		{Scope: "internal/gateway/clientmeta", Imports: "net/http os", Files: "claude_validator_test.go"},
		{Scope: "internal/gateway/completion", Imports: "internal/gateway/provider", Files: "pricing_external_test.go record_external_test.go"},
		{Scope: "internal/gateway/completion", Imports: "internal/modelcatalog/provider", Files: "helpers_test.go record_external_test.go"},
		{Scope: "internal/gateway/completion", Imports: "internal/gateway/provider/modelidentity", Files: "record_external_test.go"},
		{Scope: "internal/gateway/media", Imports: "internal/gateway/rediscache", Files: "video_tasks_integration_test.go"},
		{Scope: "internal/gateway/rediscache", Imports: "internal/scheduler/rediscache", Files: "session.go"},
		{Scope: "internal/identity", Imports: "internal/billing/postgres", Files: "admin_user_external_test.go"},
		{Scope: "internal/identity", Imports: "internal/identity/postgres", Files: "admin_transactions_external_test.go"},
		{Scope: "internal/identity", Imports: "internal/identity/provider", Files: "helpers_external_test.go dingtalk_oauth_external_test.go dingtalk_profile_sync_external_test.go dingtalk_sync_runtime_external_test.go"},
		{Scope: "internal/identity/postgres", Imports: "internal/billing/postgres", Files: "auth_state.go user_repo.go"},
		{Scope: "internal/identity/postgres", Imports: "internal/usage/postgres/query", Files: "user_repo.go"},
		{Scope: "internal/moderation", Imports: "internal/moderation/provider", Files: "helpers_test.go"},
		{Scope: "internal/moderation/postgres", Imports: "internal/identity/postgres", Files: "repository_test.go"},
		{Scope: "internal/payment/postgres", Imports: "internal/billing/postgres", Files: "checkout_scenario_test.go"},
		{Scope: "internal/routing", Imports: "internal/modelcatalog/provider", Files: "marketplace_external_test.go"},
		{Scope: "internal/routing", Imports: "internal/provider/provider", Files: "group_admin_external_test.go"},
		{Scope: "internal/routing", Imports: "internal/gateway/provider/modelidentity", Files: "marketplace_external_test.go"},
		{Scope: "internal/scheduler/rediscache", Imports: "internal/provider/provider", Files: "snapshot_integration_test.go snapshot_encoding_scenario_test.go"},
		{Scope: "internal/search", Imports: "internal/search/provider", Files: "config_test.go"},
		{Scope: "internal/setup", Imports: "internal/app/bootstrap", Files: "setup.go"},
		{Scope: "internal/team/postgres", Imports: "internal/apikey/postgres internal/billing/postgres", Files: "team_test.go"},
		{Scope: "internal/team/postgres", Imports: "internal/usage/postgres/query", Files: "team.go"},
		{Scope: "internal/upstream/usagecontract", Imports: "net/http", Files: "request.go"},
		{Scope: "internal/usage/postgres", Imports: "internal/audit/postgres", Files: "aggregation_transactions_scenario_integration_test.go"},
		{Scope: "internal/usage/postgres", Imports: "internal/billing/postgres", Files: "helpers_test.go support.go"},
		{Scope: "internal/usage/postgres", Imports: "internal/apikey/postgres internal/identity/postgres internal/routing/postgres", Files: "support.go"},
	}

	permissionSubscopes = map[string]string{
		"internal/mcp": "internal/mcp/httpapi internal/mcp/rediscache",
		"internal/provider": `internal/provider/httpapi internal/provider/postgres internal/provider/provider
internal/provider/rediscache internal/provider/transfer internal/provider/usageview`,
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

	permissionChildImports = map[string]string{
		"internal/mcp/httpapi":             "ent/runtime entgo.io/ent/dialect/sql",
		"internal/gateway/provider":        "internal/modelcatalog/provider internal/modelcatalog/testkit",
		"internal/testutil/rediscontainer": "github.com/testcontainers/testcontainers-go/modules/redis github.com/testcontainers/testcontainers-go/wait",
		"internal/gateway/clientmeta":      "net/http/httptest",
		"internal/gateway/completion":      "internal/gateway/provider/modelidentity",
		"internal/routing":                 "internal/gateway/provider/modelidentity",
	}
)
