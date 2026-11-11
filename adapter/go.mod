module github.com/cnxysoft/DDBOT-WSa/adapter

go 1.26.2

replace (
	github.com/Sora233/MiraiGo-Template => ../bot
	github.com/cnxysoft/DDBOT-WSa/utils => ../utils
	github.com/cnxysoft/DDBOT-WSa/utils/qqlog => ../utils/qqlog
)

require (
	github.com/Sora233/MiraiGo-Template v0.0.0-20250614161613-2c6ee7380548
	github.com/cnxysoft/DDBOT-WSa/utils/qqlog v0.0.0
	github.com/gorilla/websocket v1.5.3
	github.com/sirupsen/logrus v1.9.4
	github.com/stretchr/testify v1.11.1
	go.uber.org/atomic v1.11.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.4.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/sagikazarmark/locafero v0.10.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.14.0 // indirect
	github.com/spf13/cast v1.9.2 // indirect
	github.com/spf13/pflag v1.0.7 // indirect
	github.com/spf13/viper v1.20.1 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
