# UI-spec: Updates & uitrol (voor Stitch-redesign)

Doel: het updates-beheer zo helder als de inspoelstraat. Model = Intune/Autopatch
(bewezen), interface gelikter, plus onze eigen features (soak, drempels, pauze,
stragglers, boot-rollback). Zie docs/design/delivery-process.md §7-8 voor de
besluiten; dit document beschrijft alleen de schermen.

## Begrippenkader (belangrijk voor alle schermen)

Twee soorten wijzigingen, twee reizen:

- **Updates** (core/image: nieuwe NixOS-release, security-patches) raken ALLE
  devices → volledige uitrol-ladder: testdevices eerst, daarna percentage-waves.
- **Changes** (instellingen: org/groep/device-scope) raken alleen hun scope →
  testwave eerst, daarna alleen de geraakte scope. Géén vloot-ladder.

De per-groep-ladder als primair begrip verdwijnt uit de UI; groepen zijn een
implementatiedetail van de percentage-waves (een wave bestaat uit hele groepen,
kleinste eerst, tot het percentage bereikt is).

## Scherm 1: Org → tegel "Updates-beleid" (/org/updates)

Set-en-forget; drie keuzes, verder niets. Structuur (genummerde stappen):

1. **Card "Testdevices"** (stap 1)
   - Eén select: kies de testgroep. Helptekst: "Deze devices krijgen elke
     update altijd eerst, op echte hardware, met handmatig aftekenen voordat de
     vloot volgt. Meestal ICT's eigen machines."
   - Badge toont huidige testgroep + aantal devices.
2. **Card "Uitrol-ladder"** (stap 2)
   - Percentage-invoer met presets als klikbare chips: `10 · 30 · 60`
     (aanbevolen), `10 · 20 · 30 · 40`, `25 · 75`, en "eigen verdeling"
     (vrij veld). Eén knop: "Plan afleiden".
   - **Live plan-preview** daaronder: per wave een rij met wave-naam
     ("Wave 1 · 10%"), de groepen die erin vallen, het aantal devices en het
     ECHTE percentage (groepsgranulariteit ≈ gevraagd percentage). Testwave
     bovenaan, visueel onderscheiden (groen accent + aftekenen-icoon).
3. **Card "Onderhoudsvenster"** (stap 3 — nog te bouwen UI)
   - Per groep een venster "HH:MM–HH:MM" (bestaat al als setting
     dawo.updates.maintenanceWindow); default "altijd".
4. **Card "Governance"** — de drie vinkjes (change-request verplicht,
   vier-ogen, testwave verplicht). Blijft zoals nu.
5. **Details "Geavanceerd"** — handmatige wave-ladder (alleen voor
   vloot-brede uitzonderingen). Ingeklapt, bewust onopvallend.

Onzichtbare defaults (NIET in de UI): drempel 95%, soak 60/30 min,
scatter, max-in-flight, boot-health-rollback (geen uit-knop).

## Scherm 2: Sidebar "Updates" (/updates) — overzicht

- Bovenaan: samenvattingscard van de lopende uitrol (badge
  Actief/Gepauzeerd/—, actieve wave, knop "Bekijk uitrol") of een
  start-knop als er niets loopt.
- Bij starten: toon wat er uitgerold wordt en of het een **update** (hele
  vloot, volledige ladder) of een **change** (scope X, testwave + scope) is.
- Daaronder: wijzigingen-kanban (CR's) zoals nu.

## Scherm 3: /updates/rollout — monitoring

- Status-regel + Goedkeuren/Pauzeer/Hervat/Stop.
- Wave-kaarten in wizard-idioom: actieve wave gemarkeerd, progressbar,
  "Nu:"-regel in leek-taal, stragglers-uitklap (device + reden).
- Wave-labels = de ladder-namen ("Testgroep", "Wave 1 · 10%", ...).

## Motor (al gebouwd, 17 jul)

- Een wave kan meerdere groepen omvatten (`groups` naast `group`).
- Plan-afleiding: testgroep + percentages → waves met hele groepen,
  kleinste eerst (`derivePlan`), gevalideerd (groep in één wave).
- Convergentie/stragglers tellen over alle groepen van de wave.

## Nog te bouwen (na Stitch)

- Chips/presets (nu: één tekstveld), onderhoudsvenster-card,
  changes-vs-updates-classificatie bij het starten, scoped-rollout-afleiding
  (testwave + alleen geraakte scope), #88 auto-CR voor upstream-updates.
