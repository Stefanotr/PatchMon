# Fork Stefanotr/PatchMon

Ce dépôt est un fork de [PatchMon/PatchMon](https://github.com/PatchMon/PatchMon). Il suit les versions officielles et ajoute :

- la gestion des redémarrages, après un patch ou à la demande, avec leur historique, Windows compris ;
- l'activité de l'agent en direct (mise à jour de l'agent, patchs, scans) ;
- le tampon du fork : le serveur sait de quel dépôt il vient et suit les releases du fork ;
- une veille des versions officielles ;
- l'interface en français (langue du navigateur, ou choisie dans le profil ou sur la page de connexion). Les écrans principaux sont traduits, les autres restent en anglais pour l'instant.

Le patching Windows est celui de PatchMon depuis la 2.1.4 : le fork a abandonné le sien en fusionnant cette version (release `v2.1.5-fork.1`).

Le détail de ces fonctionnalités, et les liens vers leur documentation, sont dans le [README](README.md#what-this-fork-adds). Ce fichier explique comment maintenir le fork : branches, versions, releases, serveurs.

Pour traduire d'autres écrans, suis `frontend/src/i18n/README.md` : une phrase non traduite reste en anglais, et le test `translations.test.js` signale ce qui manque.

## Branches

| Branche | Rôle |
|---|---|
| `main` | Miroir exact de `PatchMon/main`. On n'y commit jamais : le bouton *Sync fork* le remet à jour. |
| `fork` | Branche par défaut et d'intégration : la base officielle plus toutes nos fonctionnalités. Les releases sont taguées dessus. |
| `feat/<sujet>`, `fix/<sujet>` | Une branche par fonctionnalité ou correctif, partie de `fork`, fusionnée dans `fork` par une pull request. Elle est supprimée après la fusion. |

Une fonctionnalité qu'on veut proposer à PatchMon part plutôt de `main`, pour que la pull request vers PatchMon ne contienne qu'elle.

## Versions

Une release du fork s'appelle `vX.Y.Z-fork.N` :

- `X.Y.Z` est la version officielle **suivant** la dernière version officielle fusionnée dans le fork ;
- `N` compte les releases du fork sur cette base.

Exemple : sur une base 2.1.3, on publie `v2.1.4-fork.1`, puis `v2.1.4-fork.2`, etc. Une version `2.1.4-fork.N` est plus haute que la 2.1.3 officielle et plus basse que la 2.1.4 officielle. Les serveurs et les agents montent donc vers chaque release du fork, et la 2.1.4 officielle passerait au-dessus.

Une version, c'est un tag, pas une branche. Le tag fige le code, et la CI garde l'image `ghcr.io/stefanotr/patchmon-server:X.Y.Z-fork.N` et la release GitHub correspondantes : on peut donc toujours revenir à une version précise. Une branche `release/X.Y` ne sert que si l'on doit corriger une ancienne version pendant que `fork` avance. On la crée alors depuis son tag, au moment où on en a besoin.

### La base réelle

Le message du tag, repris dans les notes de la release GitHub, dit sur quel code PatchMon la release est construite : `based on PatchMon v2.1.3` quand seules des versions officielles ont été fusionnées, ou `based on PatchMon v2.1.3-113-gc8f42251` quand le fork contient des commits que PatchMon n'a pas encore publiés.

**La règle : ne fusionner que des tags officiels** (`git merge vX.Y.Z`), jamais `upstream/main`. Du code PatchMon non publié peut encore changer, migrations de base comprises. `tools/fork-release.sh` refuse une telle release, et liste les migrations qu'elle apporterait. L'option `--unreleased-base` permet de passer outre en connaissance de cause.

**État actuel** : depuis la fusion de la 2.1.4 officielle (release `v2.1.5-fork.1`), le fork ne contient plus que du code PatchMon publié : `tools/fork-release.sh --push` suffit, sans `--unreleased-base`.

**Historique** : de sa première release à `v2.1.4-fork.14`, le fork contenait du code de `PatchMon/main` que PatchMon n'avait pas encore publié, même si les tags de ces releases disent « based on v2.1.3 » :

| Release | Base réelle | Migrations 2.1.4 incluses |
|---|---|---|
| `v2.1.4-fork.2` | `v2.1.3-24-g05576062` | `000048_v2-1-4_body_limit_settings` |
| `v2.1.4-fork.3` à `fork.14` | `v2.1.3-113-gc8f42251` (fusion de `136fff6`) | `000048`, `000049_v2-1-4_hosts_machine_id_index` |

Les builds faits avant `fork.2` (les binaires de `deploy-patchmon-custom.sh`, la première `fork.1` dont le tag a été supprimé) partaient de la même branche et contenaient déjà `000048`. Conséquences :

1. La base du serveur a déjà ces migrations. Revenir à l'image officielle 2.1.3 demande de restaurer une sauvegarde faite quand le serveur tournait encore sur l'image officielle, avant tout build du fork. Une sauvegarde faite sous une release du fork ne suffit pas : l'image 2.1.3 refuse de démarrer sur une base en version 48 ou 49.
2. PatchMon n'a pas modifié ces deux migrations dans la 2.1.4 officielle : vérifié à la fusion, la commande suivante n'affiche rien.
   ```bash
   git diff c8f42251 v2.1.4 -- server-source-code/internal/migrate/migrations/000048_* server-source-code/internal/migrate/migrations/000049_*
   ```

**Colonnes propres au fork** : le fork n'ajoute jamais de migration numérotée, car elle entrerait en collision avec celles de PatchMon. Ses changements de base sont des étapes idempotentes (`server-source-code/internal/forkschema`), lancées au démarrage après les migrations. Aujourd'hui, il n'y en a qu'une : la colonne `can_restart_hosts` des rôles.

## Le tampon du fork

La CI (`.github/workflows/docker.yml`) grave dans le binaire du serveur le dépôt et le commit d'où il est construit (`internal/forksource`) :

- **build officiel** (`PatchMon/PatchMon`) : rien ne change, les vérifications de mise à jour utilisent les enregistrements DNS de patchmon.net ;
- **build du fork** : le serveur lit les releases GitHub du fork pour savoir si une mise à jour existe, pour lui comme pour les agents. Les liens « release » et « repository » de l'interface pointent vers le fork. *Settings > Server Version* affiche « Fork build: Stefanotr/PatchMon @ commit, based on PatchMon/PatchMon ».

Le serveur interroge l'API GitHub sans jeton, et GitHub limite ces appels à 60 par heure et par adresse IP. Toutes les vérifications passent donc par un même cache :

- les vérifications automatiques (alertes, pages) réutilisent une réponse pendant un quart d'heure, et une minute seulement après une erreur ;
- le bouton *Check for updates* relit les releases, mais au plus une fois par minute : une release publiée apparaît tout de suite, et cliquer en boucle ne dépasse pas la limite.

Si le fork devient privé, la vérification échoue proprement : il faudra alors un jeton.

## Mise en place (une seule fois)

Les étapes 1 à 5 sont faites. Elles restent ici pour refaire un fork de zéro.

1. **Mettre en place les branches.** Renomme `feat/windows-patching-dry-run` en `fork` depuis la liste des branches : onglet *Code*, menu des branches, *View all branches*, crayon *Rename branch*. GitHub garde une redirection depuis l'ancien nom et `fork` reste la branche par défaut.
   - La branche par défaut compte : GitHub ne lance les workflows planifiés, et n'affiche le bouton *Run workflow*, qu'avec les fichiers de cette branche.
   - Ensuite, dans ton clone :
     ```bash
     git fetch origin
     git branch -m feat/windows-patching-dry-run fork
     git branch -u origin/fork fork
     git remote set-head origin -a
     ```
   - Laisse `main` identique à PatchMon. Le bouton *Sync fork* le met à jour quand tu es sur `main`.
2. **Activer les Actions.** Ça se fait dans l'onglet *Actions* du dépôt, à côté de *Pull requests*, pas dans *Settings*. Un fork y affiche « Workflows aren't being run on this forked repository » : clique sur *I understand my workflows, go ahead and enable them*.
   - Sans ce bandeau, va dans *Settings > Actions > General > Actions permissions*, choisis *Allow all actions and reusable workflows*, puis *Save*.
   - GitHub ne liste un nouveau workflow qu'après un push sur la branche par défaut. Une fois `fork` poussée, ouvre « Upstream release watch » dans la liste à gauche. S'il affiche « This scheduled workflow is disabled », clique sur *Enable workflow*. Lance-le une fois avec *Run workflow* pour vérifier.
3. **Désactiver les workflows réservés à PatchMon**. Sans leurs secrets ils échouent, et ils n'ont rien à faire dans un fork. Dans l'interface : chaque workflow, menu *...*, puis *Disable workflow*. Ou :
   ```bash
   for wf in agent-release.yml discord-activity.yml discord-release.yml \
             notify-website.yml vcheck-dns.yml ensure-labels.yml; do
     gh workflow disable "$wf" -R Stefanotr/PatchMon
   done
   ```
4. **Ajouter le dépôt officiel** comme remote dans ton clone local :
   ```bash
   git remote add upstream https://github.com/PatchMon/PatchMon.git
   ```
5. **Publier la première release** (voir plus bas). Ensuite, rends l'image publique dans *github.com/users/Stefanotr/packages/container/patchmon-server/settings*. Autre option : la laisser privée et faire `docker login ghcr.io` sur le serveur avec un jeton qui n'a que le droit `read:packages`.
6. **Protéger les branches et les tags.** Va dans *Settings > Rules > Rulesets*, puis crée trois rulesets avec *Enforcement status* sur *Active* :

   | Ruleset | Cible | Règles |
   |---|---|---|
   | `fork` (*New branch ruleset*) | *Include default branch* | *Restrict deletions*, *Block force pushes*, *Require a pull request before merging* avec 0 approbation (tu ne peux pas approuver ta propre PR), *Require status checks to pass* |
   | `main` (*New branch ruleset*) | *Include by pattern* : `main` | *Restrict deletions*, *Block force pushes*. Pas de PR obligatoire, sinon *Sync fork* ne passe plus. |
   | `tags` (*New tag ruleset*) | *Include by pattern* : `v*` | *Restrict updates*, *Restrict deletions* : une release publiée ne bouge plus. |

   - Pour *Require status checks to pass*, ajoute `Test (server)`, `Lint (server)`, `Build (server)`, `Frontend Tests`, `Agent Tests`, `Agent Lint`, `Agent Tests (windows-latest)`, `Agent Scripts (Windows PowerShell 5.1)` et `Secrets (Gitleaks)`. Ces checks tournent sur toutes les PR vers `fork`.
   - N'ajoute pas `Biome (frontend)` : il ne tourne pas sur une PR qui ne touche que `docker/`, et cette PR resterait bloquée.
   - Une PR ne peut alors plus être fusionnée avant la fin des checks. En cas d'urgence, ajoute le rôle *Repository admin* dans la *Bypass list* du ruleset `fork`.
   - Pour supprimer ou déplacer un tag, désactive le ruleset `tags` le temps de l'opération.

## Ajouter une fonctionnalité

```bash
git checkout fork && git pull
git checkout -b feat/mon-sujet
# ... commits ...
git push -u origin feat/mon-sujet
gh pr create --base fork --fill    # les tests et la qualité de code tournent sur la PR
```

Fusionne la PR dans `fork`, puis publie une release.

Le titre de la PR devient une ligne des notes de la release : écris-le pour celui qui lira ces notes, au format `type(zone): ce qui change`. `feat(...)` va dans *New*, `fix(...)` dans *Fixes*, le reste dans *Other changes*. Par exemple, `feat(activity): show the agent update live` donne `- Activity: show the agent update live (#9)` sous *New*. Un titre mal écrit se corrige sur la PR avant la fusion : c'est le titre au moment du merge qui compte.

## Publier une release du fork

```bash
git checkout fork
git pull
tools/fork-release.sh --push
```

Sous Windows, lance-le avec le bash de Git for Windows. Le `bash` de PowerShell est celui de WSL.

```powershell
$gitBash = Join-Path (Split-Path (Split-Path (Get-Command git).Source)) 'bin\bash.exe'
& $gitBash tools/fork-release.sh --push
```

Dans `cmd`, appelle-le par son chemin : `"%LOCALAPPDATA%\Programs\Git\bin\bash.exe"` pour une installation de Git pour l'utilisateur, `"%ProgramFiles%\Git\bin\bash.exe"` pour une installation pour toute la machine.

```bat
"%LOCALAPPDATA%\Programs\Git\bin\bash.exe" tools/fork-release.sh --push
```

Le script calcule le tag suivant, le crée sur `HEAD` et le pousse. Il refuse un commit déjà publié, et un fork qui contient du code PatchMon non publié sans `--unreleased-base` (voir « La base réelle »). `--dry-run` affiche le tag et ses notes sans rien créer : lance-le d'abord pour relire les notes.

Le message du tag, ce sont les notes de la release :

1. les PR fusionnées dans `fork` depuis la release précédente, par leur titre, rangées en *New*, *Fixes* et *Other changes*, avec les commits poussés directement sur `fork` ;
2. la base PatchMon, et son avertissement si elle contient du code non publié.

La page de connexion affiche les trois premières lignes : les nouveautés passent donc en premier.

La CI construit alors :

- l'image `ghcr.io/stefanotr/patchmon-server:<version>` et le tag flottant `:fork`, avec les agents de toutes les plateformes ;
- la release GitHub avec les binaires, les sommes de contrôle et l'attestation de provenance. Ses notes reprennent le message du tag.

Pour corriger les notes d'une release déjà publiée, modifie la release, pas le tag (le ruleset `tags` le protège) : `gh release edit v2.1.4-fork.N --notes-file notes.md`.

La release GitHub n'est créée qu'à la fin du build, environ six minutes après le push du tag : suis-le avec `gh run watch`. Les serveurs la voient ensuite au plus tard un quart d'heure après, et tout de suite avec le bouton *Check for updates* (*Settings > Server Version*).

Les serveurs jusqu'à `2.1.4-fork.4` gardent leur réponse un quart d'heure, même avec le bouton : attends ce délai, ou redémarre le conteneur.

## Serveur : installer le fork sur une nouvelle machine

On installe une release précise du fork. Le script d'installation, le `docker-compose.yml` et l'image viennent tous du même tag : rien ne dépend de `main`, ni du tag flottant `:fork`.

1. Vérifie l'image depuis ton poste, avec `gh`. L'attestation prouve qu'elle a été construite par la CI du fork, depuis ce tag :
   ```bash
   gh attestation verify oci://ghcr.io/stefanotr/patchmon-server:2.1.5-fork.1 --repo Stefanotr/PatchMon
   ```
2. Installe sur le serveur :
   ```bash
   VERSION=2.1.5-fork.1
   BASE="https://raw.githubusercontent.com/Stefanotr/PatchMon/refs/tags/v$VERSION"
   mkdir patchmon && cd patchmon
   UPSTREAM="$BASE" bash -c "$(curl -fsSL "$BASE/docker/setup-env.sh")"
   cat > docker-compose.override.yml <<YAML
   services:
     server:
       image: ghcr.io/stefanotr/patchmon-server:$VERSION
   YAML
   docker compose pull server
   docker compose up -d
   ```

Le script génère les secrets et le `docker-compose.yml`, et l'override n'en change que l'image. Pour figer l'image elle-même, et pas seulement son tag, remplace `:$VERSION` par son empreinte. Tu la trouves avec `docker buildx imagetools inspect ghcr.io/stefanotr/patchmon-server:$VERSION --format '{{json .Manifest.Digest}}'` ; elle s'écrit sous la forme `ghcr.io/stefanotr/patchmon-server@sha256:...`.

Ensuite, sur chaque hôte que PatchMon peut redémarrer, active les redémarrages à distance dans le `config.yml` de l'agent : section « Before PatchMon can restart a host » de `docs/patchmon-admin-guide.md`.

## Serveur : passer sur l'image du fork (une seule fois)

Cela remplace `deploy-patchmon-custom.sh` et ses bind mounts.

```bash
cd /opt/patchmon
# The dump holds password hashes and encrypted secrets: readable by root only.
(umask 077 && docker compose exec -T database sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' \
  | gzip > "backup-$(date +%Y%m%d-%H%M%S).sql.gz")
cat > docker-compose.override.yml <<'EOF'
services:
  server:
    image: ghcr.io/stefanotr/patchmon-server:2.1.5-fork.1
EOF
docker compose pull server
docker compose up -d --force-recreate server
rm -rf custom custom.old custom.new custom-src
```

## Serveur : mettre à jour

On change de version explicitement, après avoir vérifié la nouvelle image :

1. Sur ton poste : `gh attestation verify oci://ghcr.io/stefanotr/patchmon-server:<version> --repo Stefanotr/PatchMon`. Lis aussi les notes de la release, en particulier sa base réelle.
2. Sur le serveur :
   ```bash
   cd /opt/patchmon
   (umask 077 && docker compose exec -T database sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' \
     | gzip > "backup-$(date +%Y%m%d-%H%M%S).sql.gz")
   sed -i 's#patchmon-server:.*#patchmon-server:<version>#' docker-compose.override.yml
   docker compose pull server && docker compose up -d server
   ```
3. Sur chaque hôte : onglet *Host Info*, puis **Update Now**. Tu peux aussi activer l'auto-update, mais seulement sur les hôtes de test.

Si ton override utilise encore `:fork`, remplace-le par la version installée : un tag flottant change sans que tu le décides.

### Passer à `2.1.5-fork.1` (base PatchMon 2.1.4)

- **Le serveur d'abord, les agents ensuite.** Le patching Windows change de protocole : un agent `2.1.4-fork.N` garde l'ancien patching du fork, et le serveur refuse de lui envoyer un patch Windows (« Update the PatchMon agent on this host to patch Windows... »). Les patchs Linux et FreeBSD continuent comme avant. Mets ensuite à jour les agents Windows (**Update Now**).
- **Les runs Windows « Patch selected » encore en file d'attente ou planifiés** avant la mise à jour échouent avec « No patch targets » : le nouveau serveur ne connaît pas leurs éléments. Relance-les depuis l'assistant.
- **RDP** (si tu l'utilises) : `guacd` doit maintenant partager l'espace réseau du serveur. Récupère le `docker-compose.yml` du tag (`$BASE/docker/docker-compose.yml`, avec `BASE` comme dans « installer le fork ») et retire `GUACD_ADDRESS=guacd:4822` de ton `.env` s'il y est : le compose fixe lui-même `127.0.0.1:4822`. Sans ça, les sessions RDP échouent ; le reste fonctionne.
- **Proxys** : `TRUSTED_PROXY_RANGES` vide ne fait plus confiance qu'aux proxys en adresse privée, loopback ou CGNAT. Si ton reverse proxy joint PatchMon depuis une adresse publique, liste-le dans `TRUSTED_PROXY_RANGES` : sinon PatchMon prend son adresse pour celle de tous les clients, et le verrouillage des connexions les bloque ensemble.

## Suivre les versions officielles

Le workflow « Upstream release watch » tourne tous les jours. Quand PatchMon publie une version que la branche du fork ne contient pas encore, il ouvre une issue `PatchMon vX.Y.Z est disponible` avec le label `upstream-release`. GitHub te prévient par mail ou dans l'app.

Pour intégrer la nouvelle version :

```bash
git fetch upstream --tags
git checkout fork && git pull
git checkout -b sync/vX.Y.Z
git merge vX.Y.Z           # un tag officiel, jamais upstream/main
# résoudre les conflits, lancer les tests
git push -u origin sync/vX.Y.Z
gh pr create --base fork --head sync/vX.Y.Z --title "Merge PatchMon vX.Y.Z"
```

Le ruleset `fork` impose une pull request : la fusion passe donc par une branche `sync/vX.Y.Z`. Fusionne la PR **sans squash** (`gh pr merge --merge`) : un squash perdrait le lien avec le tag officiel, et la release suivante se croirait encore sur l'ancienne base. Puis :

```bash
git checkout fork && git pull
tools/fork-release.sh --dry-run   # relis les notes : tag v(X.Y.Z+1)-fork.1
tools/fork-release.sh --push
```

Ferme ensuite l'issue, et remets `main` à jour avec *Sync fork*.

Avant de fusionner, compare les migrations : une migration numérotée du fork n'existe pas (voir « Colonnes propres au fork »), mais une version officielle peut toucher une table que les étapes `forkschema` modifient.

Les écrans traduits sont ceux où les conflits arrivent le plus souvent : le texte anglais y est entouré de `t()`. Pour un conflit, garde le code de PatchMon, entoure de nouveau ses textes de `t("...")`, puis lance `npx vitest run src/__tests__/i18n` dans `frontend/` : le test liste les phrases à ajouter dans `frontend/src/i18n/locales/fr/`. Une phrase oubliée s'affiche simplement en anglais.

GitHub suspend les workflows planifiés d'un dépôt resté 60 jours sans activité. Pour le réactiver, lance le workflow à la main depuis l'onglet *Actions*.

## Revenir à l'image officielle

1. Supprime `docker-compose.override.yml`, puis lance `docker compose pull server && docker compose up -d server`. Attention à la base de données : l'image officielle doit connaître toutes les migrations déjà appliquées. Aujourd'hui, la base contient celles de la 2.1.4, donc il faut l'image officielle 2.1.4 ou plus récente, sinon restaure une sauvegarde faite sous l'image officielle, avant tout build du fork. La colonne `can_restart_hosts` ajoutée par le fork reste en place, sans effet sur l'image officielle.
2. Les agents en version fork sont plus hauts que l'officielle de même base. Ils ne redescendent donc pas tout seuls : **Update Now** les y ramène.
