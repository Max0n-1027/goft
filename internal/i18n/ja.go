package i18n

// japanese maps each English format to its Japanese translation.
//
// Verbs may be reordered with explicit indexes, %[2]s, but every argument the
// English uses must still be used: TestTranslationsTakeTheSameArguments
// renders each entry to make sure. Setting names, field names and the words a
// configuration file takes stay as they are, since they are what a person
// types; only the sentence around them is translated.
var japanese = map[string]string{
	// The command line: descriptions, flags and the help cobra adds.
	"Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory.\n\n" +
		"Without a command, goft runs every enabled job in its configuration and keeps\n" +
		"watching until it is stopped. --run-once runs the named jobs once instead.": "ローカルのディレクトリと FTP・SFTP・SMB サーバー、または別のディレクトリとの間でファイルを転送します。\n\n" +
		"コマンドを付けずに実行すると、設定にある有効なジョブをすべて動かし、止められるまで監視を続けます。\n" +
		"--run-once を付けると、指定したジョブを1回だけ実行します。",
	"path to the configuration file":                                                                          "設定ファイルのパス",
	"override log.level of the service and every job (debug | info | warn | error)":                           "サービスと全ジョブの log.level を上書きする (debug | info | warn | error)",
	"run the named job once and exit; repeat it to run several, one after another":                            "指定したジョブを1回だけ実行して終了する。繰り返し指定すると順に実行する",
	"list what would be transferred without transferring anything; with no --run-once, for every enabled job": "転送はせずに、転送するファイルを一覧表示する。--run-once がなければ有効なジョブすべてが対象",
	"List the jobs in the configuration":                                                                      "設定にあるジョブを一覧表示する",
	"List the jobs in the configuration: each one's name, direction, whether the\n" +
		"service runs it, how often it looks for files, and where it moves them from\n" +
		"and to. The configuration is checked first, as the service would check it.": "設定にあるジョブを一覧表示します。名前・方向・サービスが動かすかどうか・ファイルを探す間隔・\n" +
		"どこからどこへ転送するかを表示します。設定はサービスと同じように先に検査します。",
	"Check the configuration and each job's connection without transferring anything": "転送はせずに、設定と各ジョブの接続を確認する",
	"Check the configuration and each job's connection without transferring anything.\n\n" +
		"Each job is checked the way it uses each side: the side it takes files from\n" +
		"must be readable, the side it puts them on writable, and a side it deletes\n" +
		"from writable as well. With no job named, every enabled job is checked. A\n" +
		"job that cannot be used as configured makes goft test exit 2.": "転送はせずに、設定と各ジョブの接続を確認します。\n\n" +
		"各ジョブは、それぞれの側の使い方に合わせて確認します。ファイルを取る側は読めること、\n" +
		"置く側は書けること、削除する側も書けることが必要です。ジョブを指定しなければ有効な\n" +
		"ジョブをすべて確認します。設定どおりに使えないジョブがあると、goft test は 2 で終了します。",
	"NAME":       "名前",
	"DIRECTION":  "方向",
	"STATE":      "状態",
	"POLL":       "間隔",
	"FROM -> TO": "転送元 -> 転送先",
	"enabled":    "有効",
	"disabled":   "無効",
	"Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory": "ローカルのディレクトリと FTP・SFTP・SMB サーバー、または別のディレクトリとの間でファイルを転送する",
	"force human readable output on stdout":                                                         "人が読む形式の出力を必ず標準出力に出す",
	"suppress human readable output on stdout":                                                      "人が読む形式の出力を標準出力に出さない",
	"language to write in, en or ja (default: from GOFT_LANG or the locale)":                        "表示する言語。en か ja（既定は GOFT_LANG かロケールで決まる）",
	"Print the goft version":                                                                        "goft のバージョンを表示する",
	"help for %s":                                                                                   "%s のヘルプを表示する",
	"disable completion descriptions":                                                               "補完候補の説明を表示しない",
	"Help about any command":                                                                        "コマンドのヘルプを表示する",
	"Generate the autocompletion script for the specified shell":                                    "指定したシェル用の補完スクリプトを生成する",
	"Generate the autocompletion script for %s":                                                     "%s 用の補完スクリプトを生成する",
	"Help provides help for any command in the application.\n" +
		"Simply type %s help [path to command] for full details.": "任意のコマンドのヘルプを表示します。\n" +
		"詳しくは %s help [コマンドのパス] と入力してください。",
	"Generate the autocompletion script for %s for the specified shell.\n" +
		"See each sub-command's help for details on how to use the generated script.\n": "指定したシェル用に %s の補完スクリプトを生成します。\n" +
		"生成したスクリプトの使い方は、各サブコマンドのヘルプを参照してください。\n",
	"Generate the autocompletion script for the bash shell.\n\n" +
		"This script depends on the 'bash-completion' package.\n" +
		"If it is not installed already, you can install it via your OS's package manager.\n\n" +
		"To load completions in your current shell session:\n\n" +
		"\tsource <(%[1]s completion bash)\n\n" +
		"To load completions for every new session, execute once:\n\n" +
		"#### Linux:\n\n" +
		"\t%[1]s completion bash > /etc/bash_completion.d/%[1]s\n\n" +
		"#### macOS:\n\n" +
		"\t%[1]s completion bash > $(brew --prefix)/etc/bash_completion.d/%[1]s\n\n" +
		"You will need to start a new shell for this setup to take effect.\n": "bash 用の補完スクリプトを生成します。\n\n" +
		"このスクリプトには 'bash-completion' パッケージが必要です。\n" +
		"まだ入っていなければ、OS のパッケージマネージャーでインストールできます。\n\n" +
		"今のシェルで補完を読み込むには:\n\n" +
		"\tsource <(%[1]s completion bash)\n\n" +
		"新しいシェルで毎回読み込むには、次を一度実行します:\n\n" +
		"#### Linux:\n\n" +
		"\t%[1]s completion bash > /etc/bash_completion.d/%[1]s\n\n" +
		"#### macOS:\n\n" +
		"\t%[1]s completion bash > $(brew --prefix)/etc/bash_completion.d/%[1]s\n\n" +
		"設定を反映するには、新しいシェルを開いてください。\n",
	"Generate the autocompletion script for the zsh shell.\n\n" +
		"If shell completion is not already enabled in your environment you will need\n" +
		"to enable it.  You can execute the following once:\n\n" +
		"\techo \"autoload -U compinit; compinit\" >> ~/.zshrc\n\n" +
		"To load completions in your current shell session:\n\n" +
		"\tsource <(%[1]s completion zsh)\n\n" +
		"To load completions for every new session, execute once:\n\n" +
		"#### Linux:\n\n" +
		"\t%[1]s completion zsh > \"${fpath[1]}/_%[1]s\"\n\n" +
		"#### macOS:\n\n" +
		"\t%[1]s completion zsh > $(brew --prefix)/share/zsh/site-functions/_%[1]s\n\n" +
		"You will need to start a new shell for this setup to take effect.\n": "zsh 用の補完スクリプトを生成します。\n\n" +
		"シェルの補完がまだ有効になっていなければ、有効にする必要があります。\n" +
		"次を一度実行してください:\n\n" +
		"\techo \"autoload -U compinit; compinit\" >> ~/.zshrc\n\n" +
		"今のシェルで補完を読み込むには:\n\n" +
		"\tsource <(%[1]s completion zsh)\n\n" +
		"新しいシェルで毎回読み込むには、次を一度実行します:\n\n" +
		"#### Linux:\n\n" +
		"\t%[1]s completion zsh > \"${fpath[1]}/_%[1]s\"\n\n" +
		"#### macOS:\n\n" +
		"\t%[1]s completion zsh > $(brew --prefix)/share/zsh/site-functions/_%[1]s\n\n" +
		"設定を反映するには、新しいシェルを開いてください。\n",
	"Generate the autocompletion script for the fish shell.\n\n" +
		"To load completions in your current shell session:\n\n" +
		"\t%[1]s completion fish | source\n\n" +
		"To load completions for every new session, execute once:\n\n" +
		"\t%[1]s completion fish > ~/.config/fish/completions/%[1]s.fish\n\n" +
		"You will need to start a new shell for this setup to take effect.\n": "fish 用の補完スクリプトを生成します。\n\n" +
		"今のシェルで補完を読み込むには:\n\n" +
		"\t%[1]s completion fish | source\n\n" +
		"新しいシェルで毎回読み込むには、次を一度実行します:\n\n" +
		"\t%[1]s completion fish > ~/.config/fish/completions/%[1]s.fish\n\n" +
		"設定を反映するには、新しいシェルを開いてください。\n",
	"Generate the autocompletion script for powershell.\n\n" +
		"To load completions in your current shell session:\n\n" +
		"\t%[1]s completion powershell | Out-String | Invoke-Expression\n\n" +
		"To load completions for every new session, add the output of the above command\n" +
		"to your powershell profile.\n": "PowerShell 用の補完スクリプトを生成します。\n\n" +
		"今のシェルで補完を読み込むには:\n\n" +
		"\t%[1]s completion powershell | Out-String | Invoke-Expression\n\n" +
		"新しいシェルで毎回読み込むには、上のコマンドの出力を\n" +
		"PowerShell のプロファイルに追加してください。\n",

	// Choosing the language.
	"--lang: %w":    "--lang: %w",
	"GOFT_LANG: %w": "GOFT_LANG: %w",
	"%q is not a language goft speaks (%s | %s)": "%q は goft が使える言語ではありません (%s | %s)",

	// Starting a run.
	"no configuration found; looked at %v (use --config)": "設定ファイルが見つかりません。探した場所: %v（--config で指定してください）",
	"invalid configuration %s:\n%w":                       "設定 %s に誤りがあります:\n%w",
	"--log-level: %w":                                     "--log-level: %w",

	"no job is enabled in %s":                               "%s で有効なジョブがありません",
	"job %q: not run, since the run was stopped":            "ジョブ %q: 実行が止められたため、実行していません",
	"job %q: interrupted before the run was complete":       "ジョブ %q: 実行が終わる前に中断されました",
	"job %q: %d file(s) failed":                             "ジョブ %q: %d 件のファイルが失敗しました",
	"no job called %q is configured (goft jobs lists them)": "%q というジョブは設定にありません（goft jobs で一覧を確認できます）",
	"job %q: %w": "ジョブ %q: %w",

	// The Windows service.
	"Install goft as a Windows service, or remove it":             "goft を Windows サービスとして登録または削除する",
	"Register goft as a Windows service that starts with Windows": "Windows の起動時に開始する Windows サービスとして goft を登録する",
	"Register goft as a Windows service that starts with Windows.\n\n" +
		"The service runs this executable as LocalSystem with the configuration\n" +
		"--config names, or the one goft would find, and is restarted a minute after\n" +
		"it stops on an error. A service has no console, so every log must go to a\n" +
		"file; the configuration is checked for that, and for everything else, before\n" +
		"anything is registered. Run it from an administrator's prompt.": "Windows の起動時に開始する Windows サービスとして goft を登録します。\n\n" +
		"サービスはこの実行ファイルを LocalSystem で、--config で指定した設定（指定がなければ\n" +
		"goft が見つける設定）を使って実行し、エラーで止まった場合は 1 分後に再起動します。\n" +
		"サービスにはコンソールがないため、ログはすべてファイルに出力する必要があります。\n" +
		"登録する前に、この点を含めて設定全体を確認します。管理者のプロンプトから実行してください。",
	"Stop the Windows service if it is running, and remove it": "Windows サービスを実行中なら止めてから削除する",
	"name of the Windows service":                              "Windows サービスの名前",

	"service.log.path is not set": "service.log.path が設定されていません",
	"job %s: log.path is not set": "ジョブ %s: log.path が設定されていません",
	"a Windows service has no console to log to, so every log needs a file:\n%w": "Windows サービスにはログを出すコンソールがないため、すべてのログにファイルが必要です:\n%w",

	"goft could not start: %s":      "goft を開始できませんでした: %s",
	"goft is running %d job(s): %s": "goft は %d 件のジョブを実行しています: %s",
	"goft stopped":                  "goft が停止しました",
	"connect to the service manager: %w (run goft from an administrator's prompt)":           "サービス マネージャーに接続できません: %w（管理者のプロンプトから goft を実行してください）",
	"a service called %q is already installed (goft service uninstall --name %s removes it)": "%q というサービスは既に登録されています（goft service uninstall --name %s で削除できます）",
	"Transfers files as configured in %s":                                                    "%s の設定に従ってファイルを転送します",
	"register service %q: %w":                                                                "サービス %q を登録できません: %w",
	"set service %q to restart: %w":                                                          "サービス %q の再起動を設定できません: %w",
	"register event log source %q: %w":                                                       "イベント ログのソース %q を登録できません: %w",
	"Installed service %q: %s -c %s, as LocalSystem, starting with Windows.":                 "サービス %q を登録しました: %s -c %s を LocalSystem で、Windows の起動時に開始します。",
	"Start it now with: sc.exe start %s":                                                     "すぐに開始するには: sc.exe start %s",
	"no service called %q is installed: %w":                                                  "%q というサービスは登録されていません: %w",
	"Stopping service %q...":                                                                 "サービス %q を停止しています...",
	"stop service %q: %w":                                                                    "サービス %q を停止できません: %w",
	"remove service %q: %w":                                                                  "サービス %q を削除できません: %w",
	"The event log source %q was left in place: %s":                                          "イベント ログのソース %q は削除できませんでした: %s",
	"Removed service %q.":                                                                    "サービス %q を削除しました。",
	"service %q did not stop within %s":                                                      "サービス %q が %s 以内に停止しませんでした",

	// goft test.
	"config":        "設定",
	"job":           "ジョブ",
	"recv (delete)": "受信（削除）",
	"jobs that cannot be used as configured: %s":           "設定どおりに使えないジョブがあります: %s",
	"readable; not writable, which this job needs":         "読み取り可能。書き込みはできません（このジョブには必要です）",
	"readable; not writable, which this job does not need": "読み取り可能。書き込みはできません（このジョブには不要です）",
	"warning":      "警告",
	"local":        "ローカル",
	"resolve":      "解決",
	"connect":      "接続",
	"remote":       "リモート",
	"recv (list)":  "受信（一覧）",
	"send (write)": "送信（書込）",
	"OK":           "OK",
	"NG":           "NG",
	"FAILED":       "失敗",
	"writable":     "書き込み可能",
	"remote path does not exist and has no parent to check": "リモートのパスが存在せず、確かめられる親ディレクトリもありません",
	"cannot reach %s: %v": "%s に到達できません: %v",
	"%s is writable; %s will be created on first transfer": "%s に書き込めます。%s は最初の転送で作成されます",
	"probe directory left behind: %s":                      "確認用のディレクトリが残りました: %s",
	"readable and writable":                                "読み書き可能",
	"%d entries":                                           "%d 件",
	"remote path does not exist yet":                       "リモートのパスはまだ存在しません",

	// The console.
	"goft %s  %s  (dry-run)":      "goft %s  %s  （ドライラン）",
	"goft %s  %s  (%d files, %s)": "goft %s  %s  （%d 件、%s）",
	"(attempt %d)":                "（%d 回目）",
	"%d files, %s would be transferred (nothing was sent)":                                          "%d 件、%s を転送する予定です（何も送っていません）",
	"%d files: %d transferred (%s), %d skipped, %d failed, %d not started  interrupted after %.1fs": "%d 件: 転送 %d（%s）、スキップ %d、失敗 %d、未着手 %d  %.1f 秒で中断",
	"%d files: %d transferred (%s), %d skipped, %d failed  in %.1fs":                                "%d 件: 転送 %d（%s）、スキップ %d、失敗 %d  %.1f 秒",
	"ok":             "完了",
	"skipped":        "スキップ",
	"failed":         "失敗",
	"already_exists": "転送先に既にある",
	"identical":      "内容が同じ",
	"size_limit":     "サイズ上限を超過",

	// Reading and checking the configuration.
	"%s is a job file from before goft ran several jobs at once: move its settings under jobs:, as one item with a name and a direction": "%s は、goft が複数のジョブを動かすようになる前の形式のジョブファイルです。設定を jobs: の下に、name と direction を持つ1つの項目として移してください",
	"%s: %s is not a setting here (service | job_files | jobs)":                                                                          "%s: %s はここに書ける設定ではありません (service | job_files | jobs)",
	"%s: service is not a section of settings":                                                                                           "%s: service は設定の項目のまとまりではありません",
	"%s: jobs is not a list":                                     "%s: jobs が一覧になっていません",
	"%s: item %d of jobs is not a job":                           "%s: jobs の %d 番目の項目はジョブではありません",
	"%s: job_files is a list of file names and patterns":         "%s: job_files にはファイル名とパターンの一覧を書いてください",
	"job_files %q: %w":                                           "job_files %q: %w",
	"job_files: %w":                                              "job_files: %w",
	"%s: a job file holds jobs and nothing else, but it sets %s": "%s: ジョブファイルに書けるのは jobs だけですが、%s が書かれています",
	"%s.rotation %q is invalid (%s)":                             "%s.rotation %q は無効です (%s)",
	"%s.level: %v":                                               "%s.level: %v",
	"%s.fields: %q is not a field (%s)":                          "%s.fields: %q という項目はありません (%s)",
	"no jobs are configured in %s":                               "%s にジョブが1つも設定されていません",
	"job %s: %w":                                                 "ジョブ %s: %w",
	"name is required":                                           "name は必須です",
	"name %q contains a control character":                       "name %q に制御文字が含まれています",
	"direction is required (%s)":                                 "direction は必須です (%s)",
	"direction %q is invalid (%s)":                               "direction %q は無効です (%s)",
	"job %q in %s has the same name as job %q in %s (names that differ only in case count as the same)":                                            "%[2]s のジョブ %[1]q は、%[4]s のジョブ %[3]q と同じ名前です（大文字小文字だけが違う名前も同じとみなします）",
	"jobs %q and %q both take files from %s, and one of them deletes or moves what it sends, so the other would miss files or send them half gone": "ジョブ %q と %q はどちらも %s からファイルを取り、一方は送ったファイルを削除または移動します。もう一方がファイルを取りこぼすか、途中まで消えたファイルを送ることになります",
	"jobs pass files round in a loop: %s": "ジョブがファイルを循環させています: %s",
	"log file %s is written by %s and %s with different rotation settings; one file is rotated one way": "ログファイル %s に %s と %s が異なるローテーション設定で書いています。1つのファイルのローテーション方法は1つにしてください",

	// Locks.
	"another goft process is running it": "別の goft プロセスが実行中です",
	"lock directory %s: %w":              "ロックのディレクトリ %s: %w",
	"lock %s: %w":                        "ロック %s: %w",
	"lock directory %s is not a directory; set service.lock_dir":             "ロックのディレクトリ %s はディレクトリではありません。service.lock_dir を設定してください",
	"lock directory %s belongs to another account; set service.lock_dir":     "ロックのディレクトリ %s は別のアカウントのものです。service.lock_dir を設定してください",
	"lock directory %s is open to other accounts (%v); set service.lock_dir": "ロックのディレクトリ %s は他のアカウントにも開かれています（%v）。service.lock_dir を設定してください",
	"not an ordinary file":                             "通常のファイルではありません",
	"the file has other names (hard links); remove it": "このファイルには別の名前（ハードリンク）があります。削除してください",
	"job %q: %w (pid %s)":                              "ジョブ %q: %w（pid %s）",
	"read config: %w":                                  "設定ファイルを読めません: %w",
	"parse config %s: %w":                              "設定ファイル %s を解析できません: %w",
	"config %s: %w":                                    "設定ファイル %s: %w",
	"decode config %s: %w":                             "設定ファイル %s の値を読み取れません: %w",
	"environment variables are not set: %s":            "環境変数が設定されていません: %s",
	"%q is invalid (debug | info | warn | error)":      "%q は無効です (debug | info | warn | error)",
	"local.path is required":                           "local.path は必須です",
	"local.path %q: %v":                                "local.path %q: %v",
	"local.path %q is not a directory":                 "local.path %q はディレクトリではありません",
	"remote.path is required":                          "remote.path は必須です",
	"remote.port %d is out of range":                   "remote.port %d は範囲外です",
	"remote.protocol is required (%s)":                 "remote.protocol は必須です (%s)",
	"remote.protocol %q is invalid (%s)":               "remote.protocol %q は無効です (%s)",
	"remote.share is required for protocol smb":        "protocol が smb のときは remote.share が必須です",
	"remote.host is required":                          "remote.host は必須です",
	"remote.connect_timeout must be > 0":               "remote.connect_timeout は 0 より大きくしてください",
	"remote.io_timeout must be >= 0 (0 turns it off)":  "remote.io_timeout は 0 以上にしてください（0 で無効）",
	"remote.%s is not used with protocol local":        "protocol が local のときは remote.%s を使いません",
	"remote.path %q is not a directory":                "remote.path %q はディレクトリではありません",
	"remote.path %q: %v":                               "remote.path %q: %v",
	"remote.path %q is inside local.path %q: the two sides of a transfer must be separate directories": "remote.path %q が local.path %q の中にあります。転送の両側は別々のディレクトリにしてください",
	"local.path %q is inside remote.path %q: the two sides of a transfer must be separate directories": "local.path %q が remote.path %q の中にあります。転送の両側は別々のディレクトリにしてください",
	"verify %q is invalid (%s)":                    "verify %q は無効です (%s)",
	"on_exists %q is invalid (%s)":                 "on_exists %q は無効です (%s)",
	"post_action %q is invalid (%s)":               "post_action %q は無効です (%s)",
	"move_to is required when post_action is move": "post_action が move のときは move_to が必須です",
	"move_to %q: %v":                               "move_to %q: %v",
	"move_to %q must not be inside local.path %q: moved files would be picked up again on the next cycle":                                    "move_to %q を local.path %q の中にはできません。移動したファイルを次の周期でまた拾ってしまいます",
	"remove_empty_dirs needs post_action delete or move: with none the source files stay where they are, so no directory ever becomes empty": "remove_empty_dirs には post_action の delete か move が必要です。none では送信元のファイルがその場に残るので、ディレクトリが空になることはありません",
	"retry.max_attempts must be >= 1 (1 disables retrying)":                                                                                  "retry.max_attempts は 1 以上にしてください（1 で再試行しない）",
	"retry.interval must be >= 0":   "retry.interval は 0 以上にしてください",
	"retry.backoff must be >= 1":    "retry.backoff は 1 以上にしてください",
	"max_file_size_mb must be >= 0": "max_file_size_mb は 0 以上にしてください",
	"workers must be >= 1":          "workers は 1 以上にしてください",
	"poll_interval must be > 0":     "poll_interval は 0 より大きくしてください",
	"stable_duration must be >= 0":  "stable_duration は 0 以上にしてください",
	"post_action: move is only supported for send; use none or delete for recv":                                                      "post_action の move は send でしか使えません。recv では none か delete を使ってください",
	"stable_duration is 0: files that are still being written may be transferred":                                                    "stable_duration が 0 です。書き込み中のファイルを転送するおそれがあります",
	"insecure_skip_host_key_check is enabled: the remote host key is not verified":                                                   "insecure_skip_host_key_check が有効です。リモートのホスト鍵を検証しません",
	"remove_empty_dirs has nothing to do without recursive: only subdirectories are removed, and without recursion none are visited": "recursive がないと remove_empty_dirs は何もしません。削除するのはサブディレクトリだけで、再帰しなければサブディレクトリを辿らないためです",

	// Transferring.
	"the source changed while it was being transferred":                                                 "転送中に送信元が変わりました",
	"%w: %s and %s differ only in case, which the destination does not distinguish, so neither is sent": "%w: %s と %s は大文字と小文字しか違わず、転送先では区別されないため、どちらも送りません",
	"%w: %d bytes when it settled, %d read":                                                             "%w: 安定した時点では %d バイト、読み取ったのは %d バイト",
	"%w: %d bytes read, %d there now":                                                                   "%w: 読み取ったのは %d バイト、現在は %d バイト",
	"%w (could not clear the destination first: %w)":                                                    "%w（先に転送先を消せませんでした: %w）",
	"%w: it was modified after being sent, so it was left in place (%d bytes then, %d now)":             "%w: 送信後に変更されたため、元の場所に残しました（送信時 %d バイト、現在 %d バイト）",
	"unknown post_action %q":                                                                            "post_action %q は不明です",
	"post_action: move requires a local sending side":                                                   "post_action の move は、送信側がローカルのときしか使えません",
	"move target %s: %w":                                                                                "移動先 %s: %w",
	"hash source: %w":                                                                                   "送信元のハッシュを計算できません: %w",
	"hash destination: %w":                                                                              "転送先のハッシュを計算できません: %w",
	"unknown verify method %q":                                                                          "verify %q は不明な方式です",
	"size mismatch: sent %d bytes, destination has %d":                                                  "サイズが一致しません: 送ったのは %d バイト、転送先は %d バイト",
	"hash mismatch: sent %s, destination has %s":                                                        "ハッシュが一致しません: 送ったのは %s、転送先は %s",

	// Connecting.
	"unsupported protocol %q":                                      "未対応のプロトコルです: %q",
	"no usable connection within connect_timeout %v: %w":           "connect_timeout %v 以内に接続を確立できませんでした: %w",
	"remote.path %s: %w":                                           "remote.path %s: %w",
	"the connection stalled":                                       "接続が止まりました",
	"%w: nothing moved for %v, so the connection was dropped":      "%w: %v のあいだデータが流れなかったため、接続を切りました",
	"%w: nothing moved for %v, so the connection was dropped (%v)": "%w: %v のあいだデータが流れなかったため、接続を切りました（%v）",
	"dial %s: %w": "%s に接続できません: %w",
	"no ftp credentials for %s: set remote.user and remote.password, or add a machine entry to ~/.netrc": "%s の ftp の資格情報がありません。remote.user と remote.password を設定するか、~/.netrc に machine の行を追加してください",
	"ftp login as %s on %s: %w": "%[2]s に %[1]s としてログインできません: %[3]w",
	"no smb credentials for %s: set remote.user and remote.password, or register %q with cmdkey": "%s の smb の資格情報がありません。remote.user と remote.password を設定するか、cmdkey で %q を登録してください",
	"smb session with %s: %w":           "%s との SMB セッションを確立できません: %w",
	"mount share %q on %s: %w":          "%[2]s の共有 %[1]q をマウントできません: %[3]w",
	"%s is a file, not a directory: %w": "%s はディレクトリではなくファイルです: %w",
	"no sftp authentication available: set remote.private_key or remote.password, provide an IdentityFile in ssh_config, or load a key into ssh-agent": "sftp の認証手段がありません。remote.private_key か remote.password を設定するか、ssh_config に IdentityFile を書くか、ssh-agent に鍵を読み込んでください",
	"ssh handshake with %s: %w":      "%s との SSH ハンドシェイクに失敗しました: %w",
	"start sftp subsystem on %s: %w": "%s で sftp サブシステムを開始できません: %w",
	"no known_hosts file to verify the host key against; set remote.known_hosts or remote.insecure_skip_host_key_check": "ホスト鍵を検証する known_hosts ファイルがありません。remote.known_hosts か remote.insecure_skip_host_key_check を設定してください",
	"read known_hosts %s: %w":   "known_hosts %s を読めません: %w",
	"record host key in %s: %w": "ホスト鍵を %s に記録できません: %w",
	"create known_hosts %s: %w": "known_hosts %s を作成できません: %w",

	// Names Windows cannot store.
	"%q cannot be stored on windows: %s: %w":                                          "%q は Windows に保存できません: %s: %w",
	`":" would open an NTFS alternate data stream rather than name a file`:            `":" は名前ではなく NTFS の代替データストリームを開いてしまいます`,
	`"\" separates directories here rather than being part of a name`:                 `"\" はここでは名前の一部ではなく、ディレクトリの区切りになります`,
	"%q is not allowed in a file name":                                                "%q はファイル名に使えません",
	"the control character %U is not allowed in a file name":                          "制御文字 %U はファイル名に使えません",
	"a trailing dot is dropped, so the file would be stored under a different name":   "末尾のドットは取り除かれるので、別の名前で保存されてしまいます",
	"a trailing space is dropped, so the file would be stored under a different name": "末尾の空白は取り除かれるので、別の名前で保存されてしまいます",
	"%q is a reserved device name, whatever extension follows it":                     "%q は予約されたデバイス名です（拡張子が付いていても同じです）",

	// Credentials and ssh_config.
	"open netrc %s: %w":  "netrc %s を開けません: %w",
	"parse netrc %s: %w": "netrc %s を解析できません: %w",
	"%s is readable by other users (mode %04o); chmod 600 it": "%s は他のユーザーから読めます（モード %04o）。chmod 600 してください",
	"open ssh_config %s: %w":                                  "ssh_config %s を開けません: %w",
	"parse ssh_config %s: %w":                                 "ssh_config %s を解析できません: %w",
	"ssh_config Port %q: %w":                                  "ssh_config の Port %q: %w",
	"%v; none of its settings apply (only Match host and Match all are understood, so put what the job needs in the job file)":                      "%v。このファイルの設定は一切適用しません（解釈できる Match は host と all だけなので、ジョブに必要な設定はジョブファイルに書いてください）",
	"ssh_config %s %q is not supported; connecting directly":                                                                                        "ssh_config の %s %q には対応していません。直接接続します",
	"ssh_config sets StrictHostKeyChecking %s: the host key will not be verified":                                                                   "ssh_config で StrictHostKeyChecking %s が設定されています。ホスト鍵を検証しません",
	"ssh_config sets StrictHostKeyChecking %s: the key of a host not yet in %s will be accepted and recorded there; a changed key is still refused": "ssh_config で StrictHostKeyChecking %s が設定されています。%s にまだ載っていないホストの鍵は受け入れてそこに記録します。鍵が変わったホストは引き続き拒否します",
	"skipping the key %s: %v": "鍵 %s を使いません: %v",
	"it is passphrase protected and private_key_passphrase is not set":                                  "パスフレーズで保護されており、private_key_passphrase が設定されていません",
	"it is passphrase protected, private_key_passphrase is not set, and the ssh-agent does not hold it": "パスフレーズで保護されており、private_key_passphrase が設定されておらず、ssh-agent も保持していません",
	"parse private key %s: %w": "秘密鍵 %s を読み取れません: %w",

	// ssh-agent.
	"the ssh-agent at %s cannot be used, so none of its keys are offered: %v": "%s の ssh-agent を使えないため、その鍵は使いません: %v",
	"holds 1 key":                                    "鍵を 1 本保持",
	"holds %d keys":                                  "鍵を %d 本保持",
	"offers %d of the 1 key it holds":                "保持する鍵 1 本のうち %d 本を使用",
	"offers %d of the %d keys it holds":              "保持する鍵 %[2]d 本のうち %[1]d 本を使用",
	"ssh-agent %s: %w":                               "ssh-agent %s: %w",
	"ssh-agent pipe %s will not take a deadline: %w": "ssh-agent のパイプ %s に期限を設定できません: %w",
	"%w (the agent of an MSYS or Cygwin shell listens on a socket Windows cannot connect to; the OpenSSH agent for Windows listens on %s)": "%w（MSYS や Cygwin のシェルのエージェントは、Windows から接続できないソケットで待ち受けています。Windows 用 OpenSSH のエージェントは %s で待ち受けています）",
}
