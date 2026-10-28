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
	"Transfer files between a local directory and an FTP, SFTP or SMB server, or another directory": "ローカルのディレクトリと FTP・SFTP・SMB サーバー、または別のディレクトリとの間でファイルを転送する",
	"path to the job configuration file":                                       "ジョブ設定ファイルのパス",
	"override log.level (debug | info | warn | error)":                         "log.level を上書きする (debug | info | warn | error)",
	"override log.path":                                                        "log.path を上書きする",
	"force human readable output on stdout":                                    "人が読む形式の出力を必ず標準出力に出す",
	"suppress human readable output on stdout":                                 "人が読む形式の出力を標準出力に出さない",
	"language to write in, en or ja (default: from GOFT_LANG or the locale)":   "表示する言語。en か ja（既定は GOFT_LANG かロケールで決まる）",
	"list what would be transferred without transferring anything":             "転送はせずに、転送するファイルを一覧表示する",
	"Transfer from the local directory to the other side once":                 "ローカルのディレクトリから相手側へ1回転送する",
	"Transfer from the other side to the local directory once":                 "相手側からローカルのディレクトリへ1回転送する",
	"Watch a directory and transfer continuously":                              "ディレクトリを監視して転送し続ける",
	"Watch the local directory and transfer to the other side":                 "ローカルのディレクトリを監視して相手側へ転送する",
	"Watch the other side and transfer to the local directory":                 "相手側を監視してローカルのディレクトリへ転送する",
	"Print the goft version":                                                   "goft のバージョンを表示する",
	"Check the configuration and the connection without transferring anything": "転送はせずに、設定と接続を確認する",
	"help for %s":                     "%s のヘルプを表示する",
	"disable completion descriptions": "補完候補の説明を表示しない",
	"Help about any command":          "コマンドのヘルプを表示する",
	"Generate the autocompletion script for the specified shell": "指定したシェル用の補完スクリプトを生成する",
	"Generate the autocompletion script for %s":                  "%s 用の補完スクリプトを生成する",
	"Watch a directory and transfer continuously.\n\n" +
		"Choose a direction with `goft serve send` or `goft serve recv`.": "ディレクトリを監視して転送し続けます。\n\n" +
		"方向は `goft serve send` か `goft serve recv` で選びます。",
	"Check the configuration and the connection without transferring anything.\n\n" +
		"Both directions are reported, because the configuration file does not say\n" +
		"which one a job will be used for. A read-only account fails the send check\n" +
		"and still exits 0; only a remote that supports neither direction exits 2.": "転送はせずに、設定と接続を確認します。\n\n" +
		"設定ファイルからはジョブがどちらの方向に使われるか分からないので、両方向を報告します。\n" +
		"読み取り専用のアカウントでは send の確認が失敗しますが、終了コードは 0 です。\n" +
		"どちらの方向にも使えないリモートの場合だけ 2 で終了します。",
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
	"invalid configuration %s: %w":                        "設定 %s に誤りがあります: %w",
	"invalid configuration:\n%w":                          "設定に誤りがあります:\n%w",
	"--log-level: %w":                                     "--log-level: %w",
	"interrupted before the run was complete":             "実行が終わる前に中断されました",
	"%d file(s) failed":                                   "%d 件のファイルが失敗しました",

	// goft test.
	"config":       "設定",
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
	"the remote can be reached but is usable in neither direction": "リモートには接続できますが、どちらの方向にも使えません",
	"writable": "書き込み可能",
	"remote path does not exist and has no parent to check": "リモートのパスが存在せず、確かめられる親ディレクトリもありません",
	"cannot reach %s: %v": "%s に到達できません: %v",
	"%s is writable; %s will be created on first transfer": "%s に書き込めます。%s は最初の転送で作成されます",
	"not a directory": "ディレクトリではありません",
	"readable; not writable, which recv would need": "読み取り可能。書き込みはできません（recv には必要です）",
	"probe directory left behind: %s":               "確認用のディレクトリが残りました: %s",
	"readable and writable":                         "読み書き可能",
	"%d entries":                                    "%d 件",
	"remote path does not exist yet":                "リモートのパスはまだ存在しません",

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
	"read config: %w":                                 "設定ファイルを読めません: %w",
	"parse config %s: %w":                             "設定ファイル %s を解析できません: %w",
	"config %s: %w":                                   "設定ファイル %s: %w",
	"decode config %s: %w":                            "設定ファイル %s の値を読み取れません: %w",
	"environment variables are not set: %s":           "環境変数が設定されていません: %s",
	"%q is invalid (debug | info | warn | error)":     "%q は無効です (debug | info | warn | error)",
	"local.path is required":                          "local.path は必須です",
	"local.path %q: %v":                               "local.path %q: %v",
	"local.path %q is not a directory":                "local.path %q はディレクトリではありません",
	"remote.path is required":                         "remote.path は必須です",
	"remote.port %d is out of range":                  "remote.port %d は範囲外です",
	"remote.protocol is required (%s)":                "remote.protocol は必須です (%s)",
	"remote.protocol %q is invalid (%s)":              "remote.protocol %q は無効です (%s)",
	"remote.share is required for protocol smb":       "protocol が smb のときは remote.share が必須です",
	"remote.host is required":                         "remote.host は必須です",
	"remote.connect_timeout must be > 0":              "remote.connect_timeout は 0 より大きくしてください",
	"remote.io_timeout must be >= 0 (0 turns it off)": "remote.io_timeout は 0 以上にしてください（0 で無効）",
	"remote.%s is not used with protocol local":       "protocol が local のときは remote.%s を使いません",
	"remote.path %q is not a directory":               "remote.path %q はディレクトリではありません",
	"remote.path %q: %v":                              "remote.path %q: %v",
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
	"retry.interval must be >= 0":        "retry.interval は 0 以上にしてください",
	"retry.backoff must be >= 1":         "retry.backoff は 1 以上にしてください",
	"max_file_size_mb must be >= 0":      "max_file_size_mb は 0 以上にしてください",
	"workers must be >= 1":               "workers は 1 以上にしてください",
	"poll_interval must be > 0":          "poll_interval は 0 より大きくしてください",
	"stable_duration must be >= 0":       "stable_duration は 0 以上にしてください",
	"log.rotation %q is invalid (%s)":    "log.rotation %q は無効です (%s)",
	"log.level: %v":                      "log.level: %v",
	"log.fields: %q is not a field (%s)": "log.fields: %q という項目はありません (%s)",
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
