# antiaimark (Go)

[English](README.md) | [简体中文](README.zh-CN.md) | [Español](README.es.md) | **Français** | [Русский](README.ru.md)

Détecte et supprime les marques de provenance IA dans textes, images,
documents, vidéos et audios — stéganographie Unicode invisible, métadonnées d'image
C2PA/EXIF/XMP, métadonnées de conteneurs (PDF, DOCX, ODT, SVG, HTML, Markdown,
vidéo et audio best-effort) — avec des CLIs, un service HTTP + interface web, un
serveur MCP pour les IDE IA et un nettoyage automatique en arrière-plan. Go
pur, binaires statiques, aucune dépendance à l'exécution.

## Fonctionnalités

![Interface web (français) :](docs/screenshot-fr.png)

- **Texte (Couche A)** — caractères de largeur nulle, contrôles bidi,
  caractères de balise, espaces homoglyphes, plans à usage privé ; aller-retour octet-exact des entrées non UTF-8
- **Images** — métadonnées PNG/JPEG/WebP : manifestes C2PA/JUMBF, XMP
  `digitalSourceType=trainedAlgorithmicMedia`, blocs texte du générateur ; pixels intacts
- **Conteneurs** — PDF (exiftool + qpdf si présents), intérieur DOCX/ODT,
  blocs de métadonnées SVG, meta/JSON-LD HTML, frontmatter Markdown
- **Vidéo et audio** — best-effort : scan des boîtes C2PA uuid/JUMBF, atome QuickTime
  `©too`, scan de marqueurs (Suno/ElevenLabs/MusicGen…), purge `exiftool -all=`
- **Mots-clés éditeurs** — OpenAI/Imagen/Firefly/Midjourney/Stable
  Diffusion/FLUX/Ideogram/Recraft/Grok + 豆包·即梦/腾讯混元/通义万相/可灵/智谱/文心一格/海螺…
  (les balises CMS type WordPress sont conservées)
- **HTTP + interface web** — API JSON (`/inspect` `/clean`), dépôt par
  glisser-déposer avec téléchargement à usage unique pour images et vidéos
- **Serveur MCP** — outils natifs dans Claude Code/Desktop, Cursor, Windsurf, Cline, Continue, Zed…
- **5 langues** — en/zh/es/fr/ru pour les CLIs, erreurs HTTP, interface web et descriptions MCP
- **Nettoyage automatique** — seuil d'espace disque, période configurable

## Démarrage rapide

```bash
go build ./...          # tout compiler
go test ./...           # lancer la suite
./deploy.sh build       # ou : les 12 binaires dans bin/
./bin/antiaimark-server # HTTP + web sur 127.0.0.1:8765
```

Ouvrez http://127.0.0.1:8765/ et déposez-y une image ou une vidéo. Exemples CLI :

```bash
./bin/inspect-file photo.png --json      # inspection unifiée (routage auto)
./bin/clean-file   doc.docx              # écrit doc.cleaned.docx
./bin/clean-text   notes.txt --lang fr   # messages localisés
./bin/audit-dir    ~/blog                # audit agrégé d'un répertoire
```

## Déploiement

`./deploy.sh` couvre tous les cas (aussi via `make package`, `make install-systemd`) :

| Commande | Ce qu'elle fait |
| --- | --- |
| `./deploy.sh build` | compile tous les binaires de la plateforme hôte dans `bin/` |
| `./deploy.sh build-linux [amd64\|arm64\|386]` | compilation croisée de binaires linux statiques |
| `./deploy.sh package [arch]` | tarball autonome dans `dist/` (binaires + README + deploy/) |
| `./deploy.sh docker-build [tag]` | construit l'image Docker distroless |
| `./deploy.sh docker-run` | lance l'image avec les défauts de production (loopback, lecture seule, tmpfs) |
| `sudo ./deploy.sh install-systemd` | Linux bare-metal : binaires + utilisateur dédié + fichier env + unité systemd durcie |
| `sudo ./deploy.sh uninstall-systemd` | arrête et retire l'unité |

Flux bare-metal sur un serveur linux :

```bash
./deploy.sh package amd64                  # sur votre poste
scp dist/antiaimark-*-linux-amd64.tar.gz server:
ssh server 'tar xzf antiaimark-*.tar.gz && cd antiaimark && sudo ./deploy.sh install-systemd'
# configurer : sudoedit /etc/antiaimark.env   (port, clé d'API, auto-nettoyage…)
sudo systemctl restart antiaimark
```

Alternative Docker : `docker compose up -d` (bind loopback, healthcheck,
rootfs en lecture seule, capacités retirées ; réglages par environnement).

### Configuration (fichier env / environnement)

| Variable | Défaut | Signification |
| --- | --- | --- |
| `ANTIAIMARK_SERVER_HOST` | `127.0.0.1` | adresse d'écoute (loopback seul sauf reverse-proxy) |
| `ANTIAIMARK_SERVER_PORT` | `8765` | port |
| `ANTIAIMARK_SERVER_API_KEY` | vide | exige `Authorization: Bearer <clé>` si défini |
| `ANTIAIMARK_LANG` | langue système | `en` `zh` `es` `fr` `ru` |
| `ANTIAIMARK_AUTO_CLEAN` | `0` | `1` active le conciliateur en arrière-plan |
| `ANTIAIMARK_AUTO_CLEAN_INTERVAL` | `15m` | période de vérification |
| `ANTIAIMARK_AUTO_CLEAN_THRESHOLD` | `11` | % libre déclenchant le nettoyage |
| `ANTIAIMARK_AUTO_CLEAN_TTL` | `24h` | rétention des téléchargements avant éviction |

Le conciliateur ne supprime que les répertoires temporaires `wm-*` du service
lui-même et les téléchargements expirés — rien d'autre sur le disque ; les
répertoires de moins d'une heure sont protégés afin de ne jamais perturber les
requêtes en cours.

## API HTTP

| Méthode | Chemin | Rôle |
| --- | --- | --- |
| GET | `/health` `/capabilities` `/openapi.json` | vie, outils disponibles, OpenAPI 3.0.3 |
| POST | `/inspect` / `/clean` | fichier base64 en entrée, constats/octets nettoyés en sortie |
| GET | `/` | interface web (glisser-déposer, 5 langues) |
| POST | `/api/upload` → GET `/api/download/{token}` | dépôt multipart, téléchargement propre à usage unique |
| GET | `/api/i18n?lang=fr` | catalogue de messages de l'interface |

## Intégration aux IDE IA (MCP)

```bash
claude mcp add antiaimark -- /chemin/abs/vers/bin/antiaimark-mcp
# mcp.json de Cursor / Windsurf / Cline :
{ "mcpServers": { "antiaimark": { "command": "/chemin/abs/vers/antiaimark-mcp" } } }
```

Outils : `capabilities`, `inspect_file`, `clean_file`, `inspect_text`,
`clean_text` — les descriptions suivent la langue de l'IDE.

## Harness ML optionnels

La suppression au niveau pixel (CtrlRegen / MarkDiffusion), le scoring SynthID
et la détection MarkLLM s'exécutent comme adaptateurs externes lorsque
`NOAI_WATERMARK_DIR`, `MARKDIFFUSION_DIR`, `REVERSE_SYNTHID_DIR` ou
`MARKLLM_DIR` pointent vers des checkouts ; sans eux, le cœur fonctionne
autonome et `/capabilities` les signale absents.

## Architecture

Bibliothèque cœur plus façades fines (CLIs / HTTP / MCP) — voir
[README-ARCHITECTURE.md](README-ARCHITECTURE.md) (anglais) pour la
stratification et le guide d'extension.

## Licence

MIT — voir [LICENSE](LICENSE).
