#!/usr/bin/env python3
"""One-shot: add settings.mcp.cookieBridge.* keys to all locale files."""
import json
from pathlib import Path

LOCALES = Path("web/src/i18n/locales")

KEYS = {
    "settings.mcp.cookieBridge.title": {
        "en-US": "Cookie Bridge",
        "zh-CN": "Cookie 桥接",
        "zh-TW": "Cookie 橋接",
        "ja-JP": "Cookie ブリッジ",
        "ko-KR": "쿠키 브리지",
        "de-DE": "Cookie Bridge",
        "es-ES": "Cookie Bridge",
        "fr-FR": "Cookie Bridge",
        "pt-BR": "Cookie Bridge",
        "ru-RU": "Cookie Bridge",
    },
    "settings.mcp.cookieBridge.desc": {
        "en-US": "Move Chrome login state into an independent browser instance. Pair the Cookie Bridge Chrome extension with the token below, then add an HTTP MCP server using the MCP URL with an Authorization: Bearer <token> header.",
        "zh-CN": "把 Chrome 登录态迁移到独立浏览器实例。先用下方令牌配对 Cookie Bridge Chrome 扩展，再添加一个 HTTP MCP 服务器，URL 用 MCP URL，请求头带 Authorization: Bearer <token>。",
        "zh-TW": "把 Chrome 登入狀態遷移到獨立瀏覽器實例。先用下方權杖配對 Cookie Bridge Chrome 擴充功能，再新增一個 HTTP MCP 伺服器，URL 用 MCP URL，請求標頭帶 Authorization: Bearer <token>。",
        "ja-JP": "Chrome のログイン状態を独立ブラウザインスタンスへ移行します。まず下記トークンで Cookie Bridge 拡張機能をペアリングし、次に MCP URL を使った HTTP MCP サーバーを Authorization: Bearer <token> ヘッダー付きで追加してください。",
        "ko-KR": "Chrome 로그인 상태를 독립 브라우저 인스턴스로 옮깁니다. 먼저 아래 토큰으로 Cookie Bridge 확장 프로그램을 페어링한 뒤, MCP URL을 사용하는 HTTP MCP 서버를 Authorization: Bearer <token> 헤더와 함께 추가하세요.",
        "de-DE": "Überträgt den Chrome-Login-Zustand in eine unabhängige Browser-Instanz. Koppelte zuerst die Cookie-Bridge-Chrome-Erweiterung mit dem Token unten und füge dann einen HTTP-MCP-Server mit der MCP-URL und dem Header Authorization: Bearer <token> hinzu.",
        "es-ES": "Traslada el estado de sesión de Chrome a una instancia de navegador independiente. Empareja primero la extensión de Chrome Cookie Bridge con el token de abajo y añade luego un servidor MCP HTTP con la URL MCP y la cabecera Authorization: Bearer <token>.",
        "fr-FR": "Transfère l'état de connexion Chrome vers une instance de navigateur indépendante. Appariez d'abord l'extension Chrome Cookie Bridge avec le jeton ci-dessous, puis ajoutez un serveur MCP HTTP avec l'URL MCP et l'en-tête Authorization : Bearer <token>.",
        "pt-BR": "Move o estado de login do Chrome para uma instância de navegador independente. Primeiro pareie a extensão do Chrome Cookie Bridge com o token abaixo e depois adicione um servidor MCP HTTP usando a URL MCP com o cabeçalho Authorization: Bearer <token>.",
        "ru-RU": "Переносит состояние входа Chrome в независимый экземпляр браузера. Сначала сопрягите расширение Cookie Bridge с токеном ниже, затем добавьте HTTP MCP-сервер с URL MCP и заголовком Authorization: Bearer <token>.",
    },
    "settings.mcp.cookieBridge.mcpUrl": {
        "en-US": "MCP URL",
        "zh-CN": "MCP URL",
        "zh-TW": "MCP URL",
        "ja-JP": "MCP URL",
        "ko-KR": "MCP URL",
        "de-DE": "MCP-URL",
        "es-ES": "URL de MCP",
        "fr-FR": "URL MCP",
        "pt-BR": "URL do MCP",
        "ru-RU": "URL MCP",
    },
    "settings.mcp.cookieBridge.extUrl": {
        "en-US": "Extension URL",
        "zh-CN": "扩展 URL",
        "zh-TW": "擴充功能 URL",
        "ja-JP": "拡張機能 URL",
        "ko-KR": "확장 프로그램 URL",
        "de-DE": "Erweiterungs-URL",
        "es-ES": "URL de la extensión",
        "fr-FR": "URL de l'extension",
        "pt-BR": "URL da extensão",
        "ru-RU": "URL расширения",
    },
    "settings.mcp.cookieBridge.token": {
        "en-US": "Token",
        "zh-CN": "令牌",
        "zh-TW": "權杖",
        "ja-JP": "トークン",
        "ko-KR": "토큰",
        "de-DE": "Token",
        "es-ES": "Token",
        "fr-FR": "Jeton",
        "pt-BR": "Token",
        "ru-RU": "Токен",
    },
    "settings.mcp.cookieBridge.copy": {
        "en-US": "Copy",
        "zh-CN": "复制",
        "zh-TW": "複製",
        "ja-JP": "コピー",
        "ko-KR": "복사",
        "de-DE": "Kopieren",
        "es-ES": "Copiar",
        "fr-FR": "Copier",
        "pt-BR": "Copiar",
        "ru-RU": "Копировать",
    },
    "settings.mcp.cookieBridge.copied": {
        "en-US": "Copied",
        "zh-CN": "已复制",
        "zh-TW": "已複製",
        "ja-JP": "コピーしました",
        "ko-KR": "복사됨",
        "de-DE": "Kopiert",
        "es-ES": "Copiado",
        "fr-FR": "Copié",
        "pt-BR": "Copiado",
        "ru-RU": "Скопировано",
    },
    "settings.mcp.cookieBridge.show": {
        "en-US": "Show",
        "zh-CN": "显示",
        "zh-TW": "顯示",
        "ja-JP": "表示",
        "ko-KR": "표시",
        "de-DE": "Anzeigen",
        "es-ES": "Mostrar",
        "fr-FR": "Afficher",
        "pt-BR": "Mostrar",
        "ru-RU": "Показать",
    },
    "settings.mcp.cookieBridge.hide": {
        "en-US": "Hide",
        "zh-CN": "隐藏",
        "zh-TW": "隱藏",
        "ja-JP": "隠す",
        "ko-KR": "숨기기",
        "de-DE": "Ausblenden",
        "es-ES": "Ocultar",
        "fr-FR": "Masquer",
        "pt-BR": "Ocultar",
        "ru-RU": "Скрыть",
    },
    "settings.mcp.cookieBridge.regen": {
        "en-US": "Regenerate",
        "zh-CN": "重新生成",
        "zh-TW": "重新產生",
        "ja-JP": "再生成",
        "ko-KR": "재생성",
        "de-DE": "Neu generieren",
        "es-ES": "Regenerar",
        "fr-FR": "Régénérer",
        "pt-BR": "Regenerar",
        "ru-RU": "Пересоздать",
    },
    "settings.mcp.cookieBridge.regenConfirm": {
        "en-US": "Regenerate the pairing token? The Chrome extension and MCP server config must be updated afterwards.",
        "zh-CN": "重新生成配对令牌？之后需要更新 Chrome 扩展和 MCP 服务器配置。",
        "zh-TW": "重新產生配對權杖？之後需要更新 Chrome 擴充功能和 MCP 伺服器設定。",
        "ja-JP": "ペアリングトークンを再生成しますか？後で Chrome 拡張機能と MCP サーバー設定を更新する必要があります。",
        "ko-KR": "페어링 토큰을 재생성할까요? 이후 Chrome 확장 프로그램과 MCP 서버 설정을 업데이트해야 합니다.",
        "de-DE": "Pairing-Token neu generieren? Danach müssen die Chrome-Erweiterung und die MCP-Serverkonfiguration aktualisiert werden.",
        "es-ES": "¿Regenerar el token de emparejamiento? Después hay que actualizar la extensión de Chrome y la configuración del servidor MCP.",
        "fr-FR": "Régénérer le jeton d'appairage ? L'extension Chrome et la configuration du serveur MCP devront ensuite être mises à jour.",
        "pt-BR": "Regenerar o token de pareamento? Depois é preciso atualizar a extensão do Chrome e a configuração do servidor MCP.",
        "ru-RU": "Пересоздать токен сопряжения? После этого нужно обновить расширение Chrome и конфигурацию MCP-сервера.",
    },
    "settings.mcp.cookieBridge.unavailable": {
        "en-US": "Cookie Bridge is not running. Restart the app and check the cookie_bridge_addr setting.",
        "zh-CN": "Cookie 桥接未运行。请重启应用并检查 cookie_bridge_addr 配置。",
        "zh-TW": "Cookie 橋接未執行。請重新啟動應用並檢查 cookie_bridge_addr 設定。",
        "ja-JP": "Cookie ブリッジが実行されていません。アプリを再起動し、cookie_bridge_addr 設定を確認してください。",
        "ko-KR": "쿠키 브리지가 실행 중이 아닙니다. 앱을 재시작하고 cookie_bridge_addr 설정을 확인하세요.",
        "de-DE": "Cookie Bridge läuft nicht. Starte die App neu und prüfe die Einstellung cookie_bridge_addr.",
        "es-ES": "Cookie Bridge no está en ejecución. Reinicia la app y revisa el ajuste cookie_bridge_addr.",
        "fr-FR": "Cookie Bridge n'est pas en cours d'exécution. Redémarrez l'application et vérifiez le paramètre cookie_bridge_addr.",
        "pt-BR": "O Cookie Bridge não está em execução. Reinicie o app e verifique a configuração cookie_bridge_addr.",
        "ru-RU": "Cookie Bridge не запущен. Перезапустите приложение и проверьте параметр cookie_bridge_addr.",
    },
}

for path in sorted(LOCALES.glob("*.json")):
    locale = path.stem
    data = json.loads(path.read_text(encoding="utf-8"))
    added = 0
    for key, translations in KEYS.items():
        value = translations.get(locale)
        if value is None:
            raise SystemExit(f"{locale}: no translation for {key}")
        if key not in data:
            data[key] = value
            added += 1
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"{locale}: +{added} keys")
