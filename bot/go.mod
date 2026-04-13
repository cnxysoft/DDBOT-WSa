module github.com/Sora233/MiraiGo-Template

go 1.26.2

replace (
	github.com/Mrs4s/MiraiGo => ../miraigo
	github.com/cnxysoft/DDBOT-WSa => ../
	github.com/cnxysoft/DDBOT-WSa/adapter => ../adapter
	github.com/cnxysoft/DDBOT-WSa/utils => ../utils
	github.com/cnxysoft/DDBOT-WSa/utils/qqlog => ../utils/qqlog
)

require (
	github.com/Mrs4s/MiraiGo v0.0.0-20230627090859-19e3d172596e
	github.com/cnxysoft/DDBOT-WSa v0.0.0-20250620022611-51ba1e929e90
	github.com/cnxysoft/DDBOT-WSa/adapter v0.0.0
	github.com/fumiama/go-base16384 v1.7.0
	github.com/guonaihong/gout v0.3.7
	github.com/lestrrat-go/file-rotatelogs v2.4.0+incompatible
	github.com/mattn/go-colorable v0.1.13
	github.com/pkg/errors v0.9.1
	github.com/rifflock/lfshook v0.0.0-20180920164130-b9218ef580f5
	github.com/sirupsen/logrus v1.9.4
	github.com/spf13/viper v1.20.1
	github.com/tidwall/gjson v1.18.0
	go.uber.org/atomic v1.11.0
	gopkg.ilharper.com/x/isatty v1.1.1
)

require (
	github.com/RomiChan/protobuf v0.1.1-0.20230204044148-2ed269a2e54d // indirect
	github.com/RomiChan/syncx v0.0.0-20240418144900-b7402ffdebc7 // indirect
	github.com/andybalholm/brotli v1.0.5 // indirect
	github.com/bytedance/sonic v1.9.2 // indirect
	github.com/chenzhuoyu/base64x v0.0.0-20221115062448-fe3a3abad311 // indirect
	github.com/cnxysoft/DDBOT-WSa/lsp/eventbus v0.0.0-20251103113836-bf7ecd344df7 // indirect
	github.com/ericpauley/go-quantize v0.0.0-20200331213906-ae555eb2afa4 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/fumiama/imgsz v0.0.4 // indirect
	github.com/gabriel-vasile/mimetype v1.4.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.14.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.5 // indirect
	github.com/leodido/go-urn v1.2.4 // indirect
	github.com/lestrrat-go/strftime v1.1.1 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pierrec/lz4/v4 v4.1.22 // indirect
	github.com/sagikazarmark/locafero v0.10.0 // indirect
	github.com/samber/lo v1.38.1 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.14.0 // indirect
	github.com/spf13/cast v1.9.2 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.3.0 // indirect
	golang.org/x/crypto v0.10.0 // indirect
	golang.org/x/exp v0.0.0-20230321023759-10a507213a29 // indirect
	golang.org/x/image v0.30.0 // indirect
	golang.org/x/net v0.11.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
