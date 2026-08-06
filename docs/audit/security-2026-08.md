# Security audit, augustus 2026

Gelezen tegen `main` op 2026-08-06, vóór 1.0.0 en vóór Zaanstad. Elke
bevinding verwijst naar `file:line` en is nagemeten in de draaiende
productie waar dat kon. Bevindingen zonder bewijs staan er niet in.

Werkwijze: adversarieel lezen per pad, niet per bestand. Waar een pad langs
een claim in `docs/threat-model.md` kwam, is die claim tegen de code
gehouden.

## Bevindingen

### H1 — Het gedeelde bridge-token staat aan, en het station kan niet zonder

**Wat.** `POST /api/checkin` accepteert twee bewijzen van identiteit
(`internal/http/api/checkin.go:324-333`). Het eerste is een per-device
credential dat aan de tag gebonden is - een credential van apparaat A wordt
voor tag B geweigerd, en dat is correct. Het tweede is een **gedeeld token
dat aan niets gebonden is**: wie het heeft, mag inchecken als elke tag.

Gemeten in productie: `SEXTANT_CHECKIN_TOKEN` staat in het secret
`sextant-v2` en komt via `envFrom` in de pod terecht (64 tekens, gezet).

**Waarom dit een bevinding is en geen bekend restrisico.** `threat-model.md`
beschrijft dit als R2 en noemt de sluitvoorwaarde: *"retire the shared
bridge token once every device is enrolled with its own credential"*.

Die voorwaarde is **niet** gehaald, en het is het waard om te weten welk
apparaat hem tegenhoudt. Gemeten:

- `e2e5` heeft `device-e2e5` en checkt in via het sterke pad.
- `dawo-inspoelstraat` heeft **geen** device-credential. Wel een
  `station-dawo-inspoelstraat` van soort `station`, en
  `authenticateBound` weigert op `tok.Kind != want`
  (`internal/app/cred.go:46`) - een station-credential authenticeert dus
  geen device-check-in.
- Het station checkte vandaag om 19:14 nog in (`device_status`).

Daaruit volgt dat het station zich met het **gedeelde token**
authenticeert. Het weghalen breekt zijn check-in per direct.

De oorzaak ligt niet bij de credential maar bij de levensloop: het station
is *geregistreerd* als station, nooit *ingespoeld* als apparaat, dus het
kreeg nooit de credential die de inspoelstraat uitdeelt. Dat is dezelfde
wortel als waarom het geen netrc heeft voor cache-authenticatie.

Deze correctie stond eerst andersom in dit document ("die voorwaarde is
gehaald"), op grond van de agent-code in plaats van de draaiende vloot. Ze
staat er zo omdat een securitydocument dat verkeerd ligt gevaarlijker is
dan geen document.

**Wat het geeft aan wie het heeft**, en dit is meer dan R2 beschrijft
("can report as any device"):

1. **De escrow-sleutel van elk apparaat overschrijven.** Een check-in met
   een `recoveryKey`-veld schrijft door naar `device_secrets` met
   `ON CONFLICT ... DO UPDATE SET ciphertext=EXCLUDED.ciphertext`
   (`internal/adapters/postgres/device_secrets.go:28-33`). Er is geen
   toestandscontrole - alleen een lengtegrens van 256. Het apparaat wist
   zijn eigen kopie zodra de server `X-Recovery-Key-Stored: 1` teruggeeft
   (`checkin.go:283-289`), dus de escrow is de enige kopie. Eén verzoek per
   apparaat maakt de LUKS-herstelsleutel van de hele vloot onbruikbaar.
2. **Een wipe vervalsen als uitgevoerd.** De wipe-intent rijdt mee op het
   antwoord van de check-in, inclusief een server-ondertekende nonce
   (`checkin.go:305-311`). Wie als tag X incheckt, krijgt die nonce en kan
   hem in de volgende beat echoën als ack. `verifyIntentNonce` slaagt, want
   de nonce is echt. Gevolg: de console meldt een gestolen laptop als
   gewist terwijl het toestel intact is. De replay-guard beschermt tegen
   hergebruik van een oude nonce, niet tegen iemand die zich als het
   apparaat kan authenticeren.
3. **Compliance-verklaringen vervalsen** - posture, systemd-health en
   revisie zijn allemaal zelfgerapporteerd.

**Ernst: hoog, maar begrensd.** Uitbuiten vraagt het token, en dat staat
alleen in het cluster-secret - niet op apparaten. Wie dat secret kan lezen
heeft cluster-toegang en daarmee al grotere mogelijkheden. Het blijft de
moeite waard omdat het een gratis verwijdering is die een heel
aanvalsoppervlak dichttrekt, en omdat gevolg 1 samenvalt met het
ontbrekende Postgres-backup: de escrow-sleutels hebben geen tweede kopie.

**Advies, in deze volgorde - de eerste stap is niet over te slaan.**

1. Het station een eigen device-credential geven. Zolang dat niet gebeurd
   is, sluit het gedeelde token de inspoelstraat buiten.
2. Daarna `SEXTANT_CHECKIN_TOKEN` leeghalen **en** de fallback in
   `authorized()` verwijderen, zodat een per ongeluk teruggezet token hem
   niet opnieuw opent.
3. R2 sluiten in het threat model, en gevolg 1 en 2 daar benoemen - zolang
   R2 alleen "report as any device" zegt, leest hij als hinderlijk in plaats
   van als verlies van herstelsleutels.

Stap 1 raakt de inspoelstraat en dus hardware; dit is geen wijziging om
zonder toezicht uit te rollen.

**Stand 6 augustus, 23:00 - stap 1 en 2 gedaan, met een vondst ertussen.**

Het station heeft nu een eigen device-credential. Gemeten: het token
`device-dawo-inspoelstraat` is voor het laatst gebruikt op 21:00:06.06 en
`device_status.last_seen` staat op 21:00:06.11 - dezelfde check-in, dus het
station authenticeert zich als zichzelf. Voor `e2e5` is die drift ook nul.
Geen van beide apparaten leunt nog op de brug.

Bij het uitvoeren van stap 2 bleek de fallback erger dan hierboven staat.
`authorized()` probeerde het device-credential eerst en viel bij ELKE
mislukking terug op het gedeelde token - ook voor een tag die allang een
eigen credential had. Het token was dus geen migratiepad maar een
vloot-brede impersonatiesleutel: credentials uitgeven veranderde niets aan
de blootstelling zolang de brug open stond. Alle drie de gevolgen hierboven
golden onverkort voor `e2e5`, dat het "sterke pad" al gebruikte.

Hetzelfde gold voor `POST /api/station/{tag}/report`
(`internal/http/api/station.go`), dat bovendien geen enkele test op het
brug-token had - de reden dat het onopgemerkt bleef.

Beide paden accepteren de brug nu alleen nog voor een subject **zonder**
eigen credential, en falen gesloten als de tokenstore die vraag niet kan
beantwoorden. Wat overblijft is een gewaarschuwde, per subject naar een uur
afgeknepen logregel, zodat het antwoord op "gebruikt nog iets dit token"
een meting is en geen gok. Twee tests per pad; ze zijn geverifieerd door de
guard weg te halen en rood te zien worden.

**Gesloten 7 augustus, 00:00.** `SEXTANT_CHECKIN_TOKEN` is uit het secret
`sextant-v2` verwijderd en de console herstart. De waarde is bewust NIET
bewaard: als iets erop blijkt te leunen is de juiste reparatie dat ding een
eigen sleutel geven, en een kopie maakt de verkeerde reparatie te makkelijk.

Nagemeten over de eerste minuten na de herstart: twee check-ins, beide 204,
precies zestig seconden uit elkaar, **nul** 401's en **nul** brugregels.

Stap 3 (R2 sluiten in het threat model) staat nog open.

### M1 — Dertien geldige credentials van apparaten die niet meer bestaan

**Gemeten in productie.** `api_tokens` bevat **15** niet-verlopen
device-credentials; `fleet.json` bevat **2** apparaten. Dertien horen bij
tags die uit de vloot verwijderd zijn. Ze zijn gemint op 2026-07-13 en
lopen af op **2031-07-12**: `boundCredTTL` is vijf jaar
(`internal/app/cred.go:19`), met de redenering dat apparaten lang leven en
bij herinspoelen roteren in plaats van verlopen. Dat klopt voor een
apparaat dat blijft bestaan.

**Waarom ze er nog zijn.** Niet omdat het intrekken vergeten is: beide
verwijderpaden roepen `Revoke` aan (`internal/http/web/device_ops.go:103`,
`internal/http/api/handlers.go:156`). Ze zijn er omdat de
credential-levenscyclus aan die twee handlers hangt en **niet aan het
vlootdocument**. Elke andere manier waarop een apparaat uit `fleet.json`
verdwijnt - een change request, een commit in de overlay, een terugzetting
- laat de credential staan, en niets verzoent dat achteraf.

**Impact, eerlijk begrensd.** Een credential is aan zijn eigen tag gebonden,
dus hij kan zich niet voordoen als een bestaand apparaat. Wat hij wel kan is
inchecken als een spookapparaat en waarnemingen injecteren voor een tag die
niet in de vloot zit. Dat is precies het beeld dat vanochtend op het
overzicht stond: verwijderde apparaten die zich terugmelden. Dit is het
mechanisme dat ze blijft produceren.

**Dit is de derde keer vandaag dat hetzelfde patroon opduikt**, en dat is de
eigenlijke bevinding: de configuratie-plane en een zijstore lopen uit
elkaar, en er is geen verzoening.

| | zijstore | gevonden |
|---|---|---|
| Spookapparaten op het overzicht | observed plane | gefixt, `7cf2558` |
| Weesbranches van ingetrokken changes | git | gefixt, `32aa7bd` |
| Credentials van verwijderde apparaten | token store | **open** |

De eerste twee zijn opgelost door bij het lezen te filteren, respectievelijk
door bij het opstarten na te vegen. Voor deze derde ligt de tweede vorm
voor de hand: bij het opstarten elke device-credential intrekken waarvan de
tag niet in het vlootdocument staat, en dat luid loggen.

**Advies.** De dertien nu intrekken, en de verzoening inbouwen zodat het
niet opnieuw sluipt. Overwegen om `boundCredTTL` te verlagen: vijf jaar is
lang voor iets waarvan het intrekken aantoonbaar kan mislukken.

**Stand 6 augustus, 23:00.** De verzoening is gebouwd
(`DeviceCredentials.ReconcileWithFleet`) en zat in 0.83.0 - maar draaide
niet. De aanroep stond achter `if d.devCreds != nil`, veertig regels boven
de plek waar `devCreds` wordt toegewezen, dus de guard vond altijd nil.
Gemeten na de deploy: nog steeds 15 credentials bij 2 apparaten en geen
logregel.

Dat is dezelfde foutklasse als de rest van dit document, deze keer in de
reparatie zelf: een groene test over de verkeerde laag. De test dekte de
service, niet de bedrading. De aanroep staat nu in het Postgres-blok zonder
guard - daar bestaat de afhankelijkheid per constructie en levert
terugverplaatsen een luide start-panic op - en hij logt altijd, ook
`revoked=0`, zodat "deed niets" en "draaide niet" niet meer op elkaar
lijken.

**Gesloten 7 augustus, 00:00.** 0.84.0 draait en de sweep liep bij het
starten: **14 wezen ingetrokken**, elk met de tag erbij, gevolgd door
`device credentials reconciled against the fleet revoked=14`. `api_tokens`
gaat van 16 naar 2 device-credentials, gelijk aan het aantal apparaten in
het vlootdocument.

### H3 — Wachtwoorden van medewerkers gaan in platte tekst over het clusternetwerk

**Gemeten.** `identity.ldapUri` staat in `fleet.json` op `ldap://10.43.76.5`.
Op die tak zet de overlay-module drie dingen
(`modules/integrations.nix:334-357`):

```nix
ldap_tls_reqcert = "never";
ldap_auth_disable_tls_never_use_in_production = true;
ldap_id_use_start_tls = false;
```

De middelste is niet onze naamgeving; zo heet de optie bij SSSD. Hij staat
aan, op draaiende hardware.

**Waarom dit telt.** Een SSSD simple bind draagt het **wachtwoord van de
medewerker**, niet een hash of een token. De redenering waarom dat mocht
(route-besluit 2026-07-27) was dat de directory alleen via de WireGuard-mesh
bereikbaar is. Dat dekt het traject apparaat-naar-cluster. Van de routing
peer naar de OpenLDAP-pod gaat het verkeer plat over het podnetwerk, en
alles wat daar kan meelezen - een gecompromitteerde sidecar, een node,
`kubectl debug` - leest wachtwoorden mee terwijl ze worden ingetypt.

**Ernst: hoog.** Het gaat om het sterkste inloggegeven in het systeem, van
elke medewerker die inlogt, en het is niet begrensd tot wie clustertoegang
al heeft: meelezen op het podnetwerk is een lagere drempel dan het uitlezen
van een secret.

**Advies.** ADR 0021 besluit het: LDAPS is de ondersteunde transportlaag,
plat LDAP moet expliciet erkend worden.

**Stand 6 augustus, 23:50.** De module dwingt het af (overlay `db23306`).
Gemeten aan beide kanten op `dawo-inspoelstraat`: zonder de erkenning
weigert de evaluatie met een bericht dat de optie noemt, met de erkenning
evalueert hij en verschijnt de waarschuwing.

bb-open heeft de erkenning nu aan staan, want de directory draait vandaag
op `ldap://10.43.76.5`. **Daarmee is de bevinding niet gesloten** - hij is
zichtbaar gemaakt en staat in het vlootdocument in plaats van in een
comment. Sluiten vraagt een certificaat op de OpenLDAP-dienst en dan
`ldaps://`; dat is platformwerk. De console-bind
(`ldap://openldap.ldap-bb-open:389`) gaat in dezelfde beweging mee - die
draagt het `cn=sextant-ro`-wachtwoord, smaller maar niet anders van aard.

### H2 — De console pusht als een persoon, niet als een machine

**Gemeten.** De netrc in de console-pod authenticeert bij
`forgejo.bb-open.com` met `login bram.buijs`. Dat is het credential waarmee
de rollout-engine ring-branches force-pusht en waarmee elke commit uit de
console de forge bereikt.

**Waarom dit meer is dan R4 zegt.** `threat-model.md:151-157` beschrijft R4
als "no second factor at the git layer" en adviseert *"a dedicated
least-privilege deploy token scoped to ring refs"*. De werkelijkheid is niet
"nog niet least-privilege" maar "het account van een mens":

1. **Blast radius.** Het credential draagt alles wat die persoon op de forge
   mag, niet alleen ring-refs. Wie de pod leest, heeft dat.
2. **Het auditspoor liegt.** Elke automatische push - elke ring-promotie -
   staat in de forge op naam van een mens die op dat moment misschien niets
   deed. De vraag "wie heeft deze ring verzet" is daarmee onbeantwoordbaar,
   en dat is precies het soort vraag waarvoor een auditspoor bestaat.
3. **Sleutelpersoon-afhankelijkheid.** Vertrekt die persoon, of roteert het
   wachtwoord, dan stopt de vloot met uitrollen tot iemand het merkt.

Voor een gemeente is "de automatisering gebruikt het account van de
hoofdontwikkelaar" een bevinding in elke ISO 27001-toets, los van of het
technisch misgaat.

**Advies.** Een machine-account op de forge met schrijfrechten op deze ene
repository, en de netrc daarop. Dat is geen groot werk en het lost alle
drie de punten tegelijk op. R4 herschrijven naar wat er staat, niet naar
wat het zou moeten zijn.

### M2 — Het threat model verklaart zichzelf veilig op een voorwaarde die niet geldt

`docs/threat-model.md:310-312` sluit het risicoregister af met:

> *"None of R1-R8 is a live exploit against the deployed configuration
> (store enabled, **per-device credentials issued**, owners trusted)"*

Die tweede voorwaarde is niet waar. `dawo-inspoelstraat` heeft geen
device-credential (zie H1), en dat is precies de voorwaarde waarop R2
"geen live exploit" heet. De zin klopt voor `e2e5` en niet voor de vloot.

Dit is geen woordkwestie. Het is de enige zin in het document die een lezer
- een auditor, een gemeente, een collega - vertelt of de opgesomde randen
theorie of praktijk zijn, en hij is nooit tegen de draaiende omgeving
gehouden.

**Advies.** De zin vervangen door iets dat per risico zegt of zijn
voorwaarde geldt, en die controle onderdeel maken van de release in plaats
van van iemands geheugen. Een register dat zijn eigen aannames niet toetst,
veroudert precies zoals `1.0-fit-gap.md` deed.

### L1 — Regelverwijzing in het threat model is verlopen

`docs/threat-model.md:114` citeert `checkin.go:150-153` voor de
gedeeld-token-vergelijking; die staat nu op `checkin.go:324-333`. Klein,
maar het is dezelfde soort drift waardoor `1.0-fit-gap.md` twee weken lang
verkeerde dingen beweerde. Een verwijzing die niet meer klopt maakt de
volgende lezer trager, en de lezer daarna wantrouwig.

## Nagekeken en in orde

- **Autorisatie per verzoek.** Elke muterende web-handler roept
  `requireWeb` aan, direct of via `requireDeviceEditor`
  (`internal/http/web/device_ops.go:38`). Een eerste scan gaf vier
  destructieve device-handlers als vals alarm; die gebruiken de helper.
- **Tokens.** argon2id, hash-only opgeslagen, constant-time vergelijking
  (`internal/domain/token/token.go:149-151`), verplicht positieve TTL - geen
  eeuwige tokens - en het geheim draagt zijn eigen id, zodat verificatie
  precies één record opzoekt in plaats van de tabel te scannen.
- **Apparaat-identiteit langs het sterke pad.** Een per-device credential is
  aan de tag gebonden en wordt voor een andere tag geweigerd, met die
  bedoeling expliciet in het commentaar.
- **Wipe-ack replay.** Ondertekende nonce plus tijdstempel, en een ack die
  niet verifieert laat de beat staan maar gooit de uitkomst weg
  (`checkin.go:256-262`) - dus een vervalste ack vervuilt het auditspoor
  niet. Dat is de goede volgorde.
- **Ingetrokken apparaten.** Een retired tag krijgt 410 vóór enige
  verwerking (`checkin.go:245-249`): levenscyclus gaat voor authenticatie.
- **De nix-gate als injectie-firewall.** Hostnamen worden op het splice-punt
  zelf tegen `hostRe` gehouden en met `%q` geciteerd
  (`internal/adapters/nix/gate.go:254-264`), met in het commentaar expliciet
  waarom: *"this function must not trust that a caller upstream did its
  job"*. Uitvoeren gaat via een argv-slice, geen shell. Instellingswaarden
  bereiken nix als JSON-data, niet als expressie.
- **CSRF, structureel.** Alle 67 POST-routes gaan door één wrapper
  (`internal/http/web/web.go:146-148` -> `action`), die constant-time
  vergelijkt (`middleware.go:113`). Geen enkele route registreert zich
  buiten die helper om, dus dit is geen conventie die een nieuwe handler kan
  vergeten - het verschil met R3, waar read-confidentiality wél per handler
  wordt afgesproken.
- **Read-confidentiality (R3).** De claim "all current handlers comply"
  houdt stand. Elke API-lezer die scope-data teruggeeft filtert via
  `VisibleTo` of `canView`; de vier handlers die dat niet doen zijn
  zelf-scoped (`getMe`, `getMyPrefs`, `getTokens` - die laatste lijst
  uitsluitend `p.user.Subject`) of vragen Owner (`getDirectoryGroups`).
  Het blijft een conventie in plaats van een structurele garantie - anders
  dan CSRF, dat via één wrapper loopt - maar hij wordt vandaag nagekomen.
- **Padtraversal.** `Repo.safePath` (`internal/adapters/git/git.go:121-140`)
  doet het in twee lagen: eerst lexicaal (`filepath.Rel` plus een
  `..`-prefixcontrole), daarna **symlinks oplossen** op de dichtstbijzijnde
  bestaande voorouder en opnieuw bevestigen dat het pad onder de repo-root
  blijft. Die tweede laag is waar de meeste implementaties op stuklopen: een
  lexicale controle alleen wordt verslagen door een symlink. Dit is het pad
  waar de overlay-editor (ADR 0014) schrijft, dus het is precies de plek waar
  het moet zitten. `changeFile` valideert de id vóór de join
  (`internal/adapters/state/state.go:113-118`), en de secret-referentie in
  `cmd/sextant/capabilities.go:283` gebruikt `filepath.Base`.
- **Groottelimieten.** Elk verzoek met een body loopt door
  `http.MaxBytesReader`, met per pad een eigen grens: 4 KiB voor
  device-auth, 320 KiB voor een check-in, 4 MiB voor een station-report en
  een diagnostics-bundel. Geen enkel decodeerpad zonder grens gevonden.
- **Uitgaande verbindingen.** De enige bestemming die een operator kan
  zetten is de SMTP-host (`internal/http/web/mail.go:43-45`), en dat is
  org-Owner-only. Een Owner kan sowieso de hele vlootconfiguratie herschrijven,
  dus dit is geen rechtenescalatie. Gate-runner-URL, OpenBao-adres en LDAP-URI
  komen uit de deployment-configuratie, niet uit de console.

## Restrisicoregister nagemeten

R2 en R4 zijn hierboven behandeld (R2 werd M2, R4 werd H2). De rest, gemeten
op 2026-08-06:

**R1 - "eigenaar kan willekeurige nix laten evalueren" - HOUDT STAND, en de
formulering klopt.** `WriteOverlay` (`internal/app/config_overlays.go:53`)
heeft precies een aanroeper, `web/overlays.go:99`, en die staat achter
`requireWeb(v, "org", identity.Owner)`. Het tweede pad dat verdacht leek is
het editor-vinkje `/overlays/check`: dat is even goed owner-only, en het
evalueert niets. De runner draait daar `nix-instantiate --parse`
(`cmd/gate-runner/main.go:628`), dus alleen ontleden. De enige plek waar
console-geschreven nix echt evalueert is de gate bij commit, en die commit
staat in het auditspoor - wat R1's mitigatie ook zegt.

**R5 - losse syscall-sandbox op de wipe-unit - NIET NAGEMETEN.** Zit in de
core-nix, niet in deze repo; hoort bij de hardware-ronde.

**R6 - verouderde groepsmomentopname in een persoonlijk token - GEEN
BLOOTSTELLING VANDAAG.** De code klopt met de beschrijving: er wordt alleen
gesnoeid op groepen die uit de directory zijn verdwenen
(`internal/app/token.go:149-162`), lidmaatschap-verwijderd-terwijl-groep-
bestaat blijft begrensd door de TTL van 30 dagen. Gemeten in productie:
`api_tokens` bevat **nul** tokens van soort `personal`. Alleen 16 device- en
1 station-token. Het risico is dus echt in de code en leeg in de praktijk;
het wordt pas iets zodra de eerste persoonlijke token wordt uitgegeven.

R7 staat als CLOSED en is niet opnieuw gecontroleerd.

## Tussenstand

Twee bevindingen, geen kritieke. Het beeld tot nu toe is een codebase die de
moeilijke dingen goed doet - padtraversal met symlink-resolutie, CSRF
structureel in plaats van per handler, argon2id met constant-time
vergelijking, de gate die zijn eigen aanroepers niet vertrouwt - en die
struikelt over levensloop: dingen die blijven bestaan nadat hun aanleiding
weg is. Beide bevindingen zijn daarvan een geval, en het is dezelfde vorm
als twee bugs die dezelfde dag los hiervan gevonden werden.

Dat is een geruststellender soort zwakte dan het omgekeerde. Een ontwerp met
een gat in de authenticatie repareer je niet met opruimwerk; opruimwerk is
wat dit vraagt.
