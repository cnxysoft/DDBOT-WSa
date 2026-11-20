package parser

import (
	"strings"
	"sync"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/utils"
)

type Parser struct {
	Command string
	Args    []string
	// AtTarget 记录消息开头的@
	AtTarget int64
	// AtArgs 记录命令后的@
	AtArgs []int64

	commandName   string
	commandPrefix string
	o             sync.Once
}

func (p *Parser) Parse(elems []adapter.IMessageElement) {
	if len(elems) > 0 {
		var search []adapter.IMessageElement
		if elems[0].Type() == adapter.ElementTypeReply {
			if len(elems) > 1 {
				if elems[1].Type() == adapter.ElementTypeAt {
					search = elems[2:]
				} else {
					search = elems[1:]
				}
			}
		} else {
			search = elems[:]
		}
		if len(search) > 0 && search[0].Type() == adapter.ElementTypeAt {
			if atElem, ok := search[0].(*adapter.AtSegment); ok {
				p.AtTarget = atElem.Target
			}
			search = search[1:]
		}
		var afterCmd = false
		for _, e := range search {
			if afterCmd && e.Type() == adapter.ElementTypeAt {
				if atElem, ok := e.(*adapter.AtSegment); ok {
					p.AtArgs = append(p.AtArgs, atElem.Target)
				}
			}
			if !afterCmd && e.Type() != adapter.ElementTypeAt {
				afterCmd = true
			}
		}
	}
	var buf strings.Builder
	for _, element := range elems {
		if element.Type() != adapter.ElementTypeText {
			continue
		}
		if textElem, ok := element.(*adapter.TextSegment); ok {
			text := strings.TrimSpace(strings.Replace(textElem.Content, " ", " ", -1))
			if text == "" {
				continue
			}
			buf.WriteString(text)
			buf.WriteString(" ")
		}
	}
	splitStr := utils.ArgSplit(strings.TrimSpace(buf.String()))
	if len(splitStr) >= 1 {
		p.Command = strings.TrimSpace(splitStr[0])
		for _, s := range splitStr[1:] {
			p.Args = append(p.Args, strings.TrimSpace(s))
		}
	}
}

// GetCmd 返回包括commandPrefix在内的command字符串
func (p *Parser) GetCmd() string {
	return p.Command
}

func (p *Parser) GetArgs() []string {
	return p.Args
}

func (p *Parser) GetAtArgs() []int64 {
	return p.AtArgs
}

func (p *Parser) GetCmdArgs() []string {
	result := []string{p.Command}
	result = append(result, p.Args...)
	return result
}

func (p *Parser) AtCheck() bool {
	if p.AtTarget <= 0 {
		return true
	}
	return p.AtTarget == utils.GetBot().GetUin()
}

func (p *Parser) CommandPrefix() string {
	if p == nil {
		return ""
	}
	p.match()
	return p.commandPrefix
}

// CommandName 返回command本身的名字，不包括command prefix
func (p *Parser) CommandName() string {
	if p == nil {
		return ""
	}
	p.match()
	return p.commandName
}

func (p *Parser) match() {
	p.o.Do(func() {
		var (
			err     error
			command string
			prefix  string
		)
		prefix, command, err = cfg.MatchCmdWithPrefix(p.GetCmd())
		if err != nil {
			return
		}
		p.commandPrefix = prefix
		p.commandName = command
	})
}

func NewParser() *Parser {
	return &Parser{
		Command: "",
		Args:    nil,
	}
}
