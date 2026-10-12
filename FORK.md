# Fork Stefanotr/PatchMon

Ce dépôt est un fork de [PatchMon/PatchMon](https://github.com/PatchMon/PatchMon).
Il ajoute :

- le patching Windows (Windows Update + WinGet) avec dry run, comme sur Linux ;
- la mémoire des mises à jour que WinGet refuse de faire sur place ;
- la gestion des redémarrages, après un patch ou à la demande (voir « Restarting the Host » dans `docs/patchmon-admin-guide.md`) ;
- une sécurité qui empêche l'agent de se mettre à jour pendant un patch ;
- le tampon du fork : le serveur sait de quel dépôt il vient, il cherche ses mises à jour dans les releases du fork et l'interface renvoie vers le fork.

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

## Le tampon du fork

La CI (`.github/workflows/docker.yml`) grave dans le binaire du serveur le dépôt et le commit d'où il est construit (`internal/forksource`) :

- **build officiel** (`PatchMon/PatchMon`) : rien ne change, les vérifications de mise à jour utilisent les enregistrements DNS de patchmon.net ;
- **build du fork** : le serveur lit les releases GitHub du fork pour savoir si une mise à jour existe, pour lui comme pour les agents. Les liens « release » et « repository » de l'interface pointent vers le fork. *Settings > Server Version* affiche « Fork build: Stefanotr/PatchMon @ commit, based on PatchMon/PatchMon ».

Le serveur interroge l'API GitHub sans jeton, au plus une fois tous les quarts d'heure (une fois par minute après une erreur). Si le fork devient privé, la vérification échoue proprement : il faudra alors un jeton.

## Mise en place (une seule fois)

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

## Ajouter une fonctionnalité

```bash
git checkout fork && git pull
git checkout -b feat/mon-sujet
# ... commits ...
git push -u origin feat/mon-sujet
gh pr create --base fork --fill    # les tests et la qualité de code tournent sur la PR
```

Fusionne la PR dans `fork`, puis publie une release.

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

Le script calcule le tag suivant, le crée sur `HEAD` et le pousse. La CI construit alors :

- l'image `ghcr.io/stefanotr/patchmon-server:<version>` et le tag flottant `:fork`, avec les agents de toutes les plateformes ;
- la release GitHub avec les binaires, les sommes de contrôle et l'attestation de provenance.

Les serveurs voient la nouvelle release au plus tard un quart d'heure après sa publication. Le bouton *Check for updates* force la vérification.

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
    image: ghcr.io/stefanotr/patchmon-server:fork
EOF
docker compose pull server
docker compose up -d --force-recreate server
rm -rf custom custom.old custom.new custom-src
```

## Serveur : mettre à jour

C'est le même geste qu'avec l'image officielle :

```bash
cd /opt/patchmon
docker compose pull server && docker compose up -d server
```

Ensuite, sur chaque host : onglet *Host Info*, puis **Update Now**. Tu peux aussi activer l'auto-update, mais seulement sur les hosts de test.

## Suivre les versions officielles

Le workflow « Upstream release watch » tourne tous les jours. Quand PatchMon publie une version que la branche du fork ne contient pas encore, il ouvre une issue `PatchMon vX.Y.Z est disponible` avec le label `upstream-release`. GitHub te prévient par mail ou dans l'app.

Pour intégrer la nouvelle version :

```bash
git fetch upstream --tags
git checkout fork
git merge vX.Y.Z           # résoudre les conflits, lancer les tests
git push origin fork
tools/fork-release.sh --push   # tag v(X.Y.Z+1)-fork.1
```

Puis ferme l'issue, et remets `main` à jour avec *Sync fork*.

GitHub suspend les workflows planifiés d'un dépôt resté 60 jours sans activité. Pour le réactiver, lance le workflow à la main depuis l'onglet *Actions*.

## Revenir à l'image officielle

1. Supprime `docker-compose.override.yml`, puis lance `docker compose pull server && docker compose up -d server`. Attention à la base de données : l'image officielle doit être au moins de la version de la base du fork. Sinon, restaure la sauvegarde.
2. Les agents en version fork sont plus hauts que l'officielle de même base. Ils ne redescendent donc pas tout seuls : **Update Now** les y ramène.
