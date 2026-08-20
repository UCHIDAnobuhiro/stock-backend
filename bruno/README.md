# Bruno collection

ローカルの Stock Backend API を Bruno から実行するためのコレクションです。

## 使い方

1. API を起動します。

   ```bash
   docker compose -f docker/docker-compose.yml -p stock up backend
   ```

2. Bruno の **Open Collection** で、この `bruno/` ディレクトリを選択します。
3. 必要に応じて環境 **Local** を選択します。未選択でも同じローカル用の初期値を使用します。
4. 初回だけ `01 Auth/Signup` を実行し、続けて `01 Auth/Login` を実行します。
5. 以降は Market、Watchlist、Logo の各リクエストをそのまま実行できます。

Bruno がログインレスポンスの Cookie を保存し、コレクションの pre-request script が変更系リクエストへ `X-CSRF-Token` を自動設定します。Bruno の Settings で Cookie の自動保存・送信を無効にしている場合は有効にしてください。

リクエスト値は `environments/Local.bru`、または Bruno の環境変数画面から変更できます。初期ユーザーは `bruno@example.com` / `bruno-password-123`、追加・削除対象の銘柄は `NVDA` です。

`manual` タグのリクエストには次の注意点があります。

- Logo は外部 API を使用し、ユーザー単位のレートリミットがあります。`Detect Logo` の Body で送信画像を選択してから実行してください。
- OAuth は OAuth 用環境変数を設定した API でのみルートが有効です。通常は `Begin OAuth` からブラウザでフローを進めます。
- Logout は現在の Cookie を失効・削除します。続けて API を試す場合は Login を再実行してください。

Bruno CLI を使う場合は、API の起動後に次のように実行できます。

```bash
cd bruno
bru run --env Local --exclude-tags=manual
```
