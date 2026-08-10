#!/usr/bin/env bash

# Markdown内のリポジトリ相対リンクが実在するファイル・ディレクトリを指すか検証する。
# 外部URL、メールアドレス、同一ページ内アンカーは対象外とする。
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

markdown_files=()
while IFS= read -r file; do
  markdown_files+=("$file")
done < <(git -c core.quotePath=false ls-files '*.md')

perl -MFile::Basename=dirname -MFile::Spec -e '
  my $failed = 0;
  while (<>) {
    while (/\[[^\]]*\]\(([^)#]+)(?:#[^)]*)?\)/g) {
      my $target = $1;
      $target =~ s/^<|>$//g;
      next if $target =~ m{^(?:https?://|mailto:|#)};
      next if $target eq "XXXX-title.md";

      my $path = File::Spec->rel2abs($target, dirname($ARGV));
      next if -e $path;

      print STDERR "$ARGV:$.: broken local link: $target\n";
      $failed = 1;
    }
  } continue {
    close ARGV if eof;
  }
  exit $failed;
' "${markdown_files[@]}"
