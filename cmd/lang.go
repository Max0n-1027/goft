package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"goft/internal/i18n"
)

// flagLang is --lang: the language to write in, ahead of GOFT_LANG and the
// locale.
var flagLang string

// tr renders what the command line says to a person. It starts out English
// and is settled by applyLanguage once the flags have been read.
var tr = i18n.English

// applyLanguage decides the language from --lang, GOFT_LANG and the locale,
// and puts every command's help into it.
//
// It runs once the flags are parsed, which is the earliest --lang can be
// known: before a command runs, before help is shown, and before an error is
// reported. A --lang or GOFT_LANG naming a language goft does not speak is
// returned as an error, and English is used meanwhile.
func applyLanguage() error {
	p, err := i18n.Detect(flagLang, os.Getenv)
	tr = p
	localizeCommands(rootCmd, p)
	return err
}

// original is a command's help as it was written, in English, kept so that
// the help can be put into either language however many times the commands
// run.
type original struct {
	short, long string
}

var (
	originals  = map[*cobra.Command]original{}
	flagUsages = map[*pflag.Flag]string{}
)

// localizeCommands puts the help of root and every command under it into p's
// language, including the help and completion commands cobra adds itself.
func localizeCommands(root *cobra.Command, p *i18n.Printer) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		c.InitDefaultHelpFlag()
		o, ok := originals[c]
		if !ok {
			o = original{short: c.Short, long: c.Long}
			originals[c] = o
		}
		c.Short, c.Long = o.short, o.long
		if p.Lang() != i18n.LangEnglish {
			c.Short, c.Long = translateCommand(c, o, p)
		}
		for _, fs := range []*pflag.FlagSet{c.LocalFlags(), c.PersistentFlags()} {
			fs.VisitAll(func(f *pflag.Flag) { localizeFlag(c, f, p) })
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)

	if p.Lang() == i18n.LangJapanese {
		root.SetUsageTemplate(usageTemplateJapanese)
	} else {
		root.SetUsageTemplate("")
	}
}

func localizeFlag(c *cobra.Command, f *pflag.Flag, p *i18n.Printer) {
	usage, ok := flagUsages[f]
	if !ok {
		usage = f.Usage
		flagUsages[f] = usage
	}
	switch {
	case p.Lang() == i18n.LangEnglish:
		f.Usage = usage
	case f.Name == "help" && len(f.Annotations[cobra.FlagSetByCobraAnnotation]) > 0:
		f.Usage = p.Sprintf("help for %s", c.DisplayName())
	case f.Name == "no-descriptions":
		f.Usage = p.Text(i18n.N("disable completion descriptions"))
	default:
		f.Usage = p.Text(usage)
	}
}

// translateCommand returns a command's help in p's language. goft's own
// commands mark their text with i18n.N where they are declared; the commands
// cobra adds are recognised here and given text of goft's own, worded as
// cobra words them in English.
func translateCommand(c *cobra.Command, o original, p *i18n.Printer) (short, long string) {
	root := c.Root().DisplayName()
	parent := ""
	if c.HasParent() {
		parent = c.Parent().Name()
	}
	switch {
	case c.Name() == "help" && c.Parent() == c.Root():
		return p.Text(i18n.N("Help about any command")),
			p.Sprintf("Help provides help for any command in the application.\n"+
				"Simply type %s help [path to command] for full details.", root)
	case c.Name() == "completion" && c.Parent() == c.Root():
		return p.Text(i18n.N("Generate the autocompletion script for the specified shell")),
			p.Sprintf("Generate the autocompletion script for %s for the specified shell.\n"+
				"See each sub-command's help for details on how to use the generated script.\n", root)
	case parent == "completion":
		return p.Sprintf("Generate the autocompletion script for %s", c.Name()), completionHelp(c.Name(), root, p, o.long)
	}
	return p.Text(o.short), p.Text(o.long)
}

// completionHelp is the help of the completion command for one shell.
func completionHelp(shell, root string, p *i18n.Printer, english string) string {
	switch shell {
	case "bash":
		return p.Sprintf("Generate the autocompletion script for the bash shell.\n\n"+
			"This script depends on the 'bash-completion' package.\n"+
			"If it is not installed already, you can install it via your OS's package manager.\n\n"+
			"To load completions in your current shell session:\n\n"+
			"\tsource <(%[1]s completion bash)\n\n"+
			"To load completions for every new session, execute once:\n\n"+
			"#### Linux:\n\n"+
			"\t%[1]s completion bash > /etc/bash_completion.d/%[1]s\n\n"+
			"#### macOS:\n\n"+
			"\t%[1]s completion bash > $(brew --prefix)/etc/bash_completion.d/%[1]s\n\n"+
			"You will need to start a new shell for this setup to take effect.\n", root)
	case "zsh":
		return p.Sprintf("Generate the autocompletion script for the zsh shell.\n\n"+
			"If shell completion is not already enabled in your environment you will need\n"+
			"to enable it.  You can execute the following once:\n\n"+
			"\techo \"autoload -U compinit; compinit\" >> ~/.zshrc\n\n"+
			"To load completions in your current shell session:\n\n"+
			"\tsource <(%[1]s completion zsh)\n\n"+
			"To load completions for every new session, execute once:\n\n"+
			"#### Linux:\n\n"+
			"\t%[1]s completion zsh > \"${fpath[1]}/_%[1]s\"\n\n"+
			"#### macOS:\n\n"+
			"\t%[1]s completion zsh > $(brew --prefix)/share/zsh/site-functions/_%[1]s\n\n"+
			"You will need to start a new shell for this setup to take effect.\n", root)
	case "fish":
		return p.Sprintf("Generate the autocompletion script for the fish shell.\n\n"+
			"To load completions in your current shell session:\n\n"+
			"\t%[1]s completion fish | source\n\n"+
			"To load completions for every new session, execute once:\n\n"+
			"\t%[1]s completion fish > ~/.config/fish/completions/%[1]s.fish\n\n"+
			"You will need to start a new shell for this setup to take effect.\n", root)
	case "powershell":
		return p.Sprintf("Generate the autocompletion script for powershell.\n\n"+
			"To load completions in your current shell session:\n\n"+
			"\t%[1]s completion powershell | Out-String | Invoke-Expression\n\n"+
			"To load completions for every new session, add the output of the above command\n"+
			"to your powershell profile.\n", root)
	}
	return english
}

// usageTemplateJapanese is cobra's usage template with its headings in
// Japanese. The structure is cobra's own, so a command shows the same parts in
// either language.
const usageTemplateJapanese = `使い方:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

別名:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

例:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

コマンド:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

その他のコマンド:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

フラグ:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

共通フラグ:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

その他のヘルプ:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

各コマンドの詳細は "{{.CommandPath}} [command] --help" で表示できます。{{end}}
`
