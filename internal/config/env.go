package config

// EnvVars returns the environment variables tfcensus reads besides TFCENSUS_CONFIG and the
// TFCENSUS_<KEY> config variables. A test in scripts/gendocs checks the list against the code.
func EnvVars() []EnvVar {
	return []EnvVar{
		{"AWS_PROFILE", "Profile for the default credentials when no --profile, --org or accounts setting names one."},
		{"AWS_CONFIG_FILE", "AWS shared config file to read profiles from, instead of ~/.aws/config."},
		{"AWS_SHARED_CREDENTIALS_FILE", "AWS shared credentials file, instead of ~/.aws/credentials. " +
			"Only profile names are read from it."},
		{
			"AWS_ENDPOINT_URL",
			"Endpoint for every AWS service, such as an emulator. S3 then uses path-style addressing.",
		},
		{"AWS_ENDPOINT_URL_S3", "Endpoint for S3 only. S3 then uses path-style addressing."},
		{"AWS_*", "The AWS SDK reads its usual variables too, such as AWS_REGION, AWS_ACCESS_KEY_ID and " +
			"AWS_WEB_IDENTITY_TOKEN_FILE."},
		{
			"HOME",
			"Home directory: ~/.aws/config, the config and cache directories, and ~ in discovery.roots and path arguments.",
		},
		{"USERPROFILE", "Home directory on Windows when HOME is not set."},
		{"XDG_CONFIG_HOME", "Base of the user config file: $XDG_CONFIG_HOME/tfcensus/config.yaml."},
		{"XDG_CACHE_HOME", "Base of the cache directory: $XDG_CACHE_HOME/tfcensus."},
		{"NO_COLOR", "Turns color off with --color auto when set to anything but an empty string."},
		{"CLICOLOR_FORCE", "Turns color on with --color auto, even when output is not a terminal, unless it is 0 " +
			"or NO_COLOR is set."},
		{"FORCE_COLOR", "Turns color on like CLICOLOR_FORCE unless it is 0 or false."},
		{"CLICOLOR", "Set to 0, turns color off with --color auto."},
		{"TERM", "Set to dumb, turns color off with --color auto."},
		{"COLUMNS", "Width in columns that tty output wraps notes and warnings at; default: the terminal's width."},
		{
			"CI",
			"Any value turns prompts off, as no terminal does: move apply, rollback and mark-applied then need --yes.",
		},
		{"TG_IAM_ASSUME_ROLE", "Read like terragrunt does: when set, the fast path leaves get_aws_account_id to " +
			"terragrunt render, since terragrunt would assume that role first."},
		{"TERRAGRUNT_IAM_ROLE", "The older name of TG_IAM_ASSUME_ROLE, read the same way."},
		{"PATH", "Where terragrunt, terraform and tofu are looked up, in the environment the child process gets."},
		{"PATHEXT", "File extensions of programs on Windows, for the same lookup."},
		{"SSL_CERT_FILE", "PEM file with more CA certificates for notify's HTTPS requests, such as a proxy's."},
		{
			"GITHUB_REPOSITORY",
			"Repository for the github-issues and github-pr-comment notifiers when the config names none.",
		},
		{"GITHUB_API_URL", "GitHub API URL for the GitHub notifiers when the config names none."},
		{
			"GITHUB_EVENT_PATH",
			"Event file that github-pr-comment reads the pull request number from in GitHub Actions.",
		},
		{"BITBUCKET_WORKSPACE", "Workspace for bitbucket-pr-comment when the config names none."},
		{"BITBUCKET_REPO_SLUG", "Repository for bitbucket-pr-comment when the config names none."},
		{"BITBUCKET_PR_ID", "Pull request for bitbucket-pr-comment when the config names none."},
		{githubTokenEnv, "Token for the GitHub notifiers, the default of notify.github_issues.token_env and " +
			"notify.github_pr_comment.token_env."},
		{"SLACK_WEBHOOK_URL", "Webhook URL for the slack notifier, the default of notify.slack.webhook_env."},
		{"BITBUCKET_TOKEN", "Token for bitbucket-pr-comment, the default of notify.bitbucket_pr_comment.token_env."},
	}
}
