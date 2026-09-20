# sporemind End User License Agreement (Draft)

> **Document status: DRAFT**
>
> This document is a **draft of the sporemind End User License Agreement ("EULA")**. It is provided for review and discussion only and **must be reviewed by legal professionals before it takes effect**. Until finalized after such review, it creates no legally binding obligations on any party.
>
> - EULA version: `v0.1-draft`
> - Effective date: `TBD (YYYY-MM-DD)`
> - Product covered: sporemind, a multi-agent collaboration runtime (including desktop / headless CLI, Web, and mobile forms)
> - Stance: The public license for sporemind itself is **not yet specified**; the governing license must be determined and this document updated accordingly before these terms are put into force.

---

## 0. Draft Statement

0.1 This document is a **draft** of the sporemind End User License Agreement (the "Agreement"). Items marked "[TBD]" are open placeholders that must be determined by the licensor before release.

0.2 This draft does not represent the final position of sporemind or its rights holders, does not constitute an offer or commitment, and grants no rights. No party may rely on this draft to claim any implied authorization.

0.3 In the event of any conflict between this draft and the final version, the final version prevails.

---

## 1. Definitions

1.1 **"sporemind"** means the multi-agent collaboration runtime software developed and operated by the rights holder, together with all of its components, documentation, and deliverables, including but not limited to the desktop application, the headless command-line program, the built-in Web frontend, the mobile client, plugins (Native Plugins / Spore Apps), themes, and related assets.

1.2 **"Rights Holder"** means the entity that holds the intellectual property rights in sporemind.

1.3 **"User"** means any individual or legal entity that installs, copies, accesses, or uses sporemind in any way.

1.4 **"Software"** means sporemind and its accompanying documentation and assets, unless the context indicates otherwise.

1.5 **"Third-Party Components"** means software components developed by third parties (other than the Rights Holder) and provided to users under their respective license terms, including open-source and commercial components.

---

## 2. Scope of License

### 2.1 License Grant

2.1.1 Subject to full compliance with this Agreement (including any supplemental terms finalized later), the Rights Holder grants the User a **non-exclusive, non-transferable, non-sublicensable, revocable** right to install, run, and use the Software on the User's own devices, for **personal or commercial purposes**.

2.1.2 [TBD] The specific scope of commercial use (e.g., internal use, providing services to third parties, SaaS deployment) and whether separate commercial licenses or paid authorization are required must be determined before finalization.

### 2.2 Product Forms

2.2.1 This Agreement applies to any form in which the User uses the Software, including but not limited to:

- **Desktop**: the desktop application (graphical interface mode) and the **headless command-line mode** (`cmd/sporemind`);
- **Web**: the Web frontend built into or distributed with the Software;
- **Mobile**: the mobile client (Capacitor packaging form).

2.2.2 All forms are part of the same Software and are governed by this Agreement. If a particular form is subject to separate additional terms, such additional terms prevail over this Agreement to the extent of any conflict.

### 2.3 License Restrictions

2.3.1 Except as expressly permitted by this Agreement or applicable law, the User must not:

1. copy, modify, translate, decompile, reverse engineer, disassemble the Software, or attempt to obtain its source code (excluding Spore App / plugin code authored by the User and running on top of the Software);
2. remove, alter, or obscure any copyright notice, trademark, logo, or other proprietary marking contained in the Software;
3. sell, rent, lend, transfer, sublicense, or commercially distribute the Software or any part of it;
4. use the Software for any activity that violates applicable law or infringes third-party rights;
5. circumvent or disable any technical protection measure, license verification, or authorization mechanism of the Software.

---

## 3. Intellectual Property Ownership

3.1 The Software (including but not limited to its source code, object code, architecture, interface, documentation, icons, brand names, and logos) and all intellectual property rights therein (including but not limited to copyrights, patents, trademarks, and trade secrets) are owned by the Rights Holder and its licensors. The grant under this Agreement does not constitute a transfer of any of those rights; the User obtains only the limited right of use described in Section 2.

3.2 Content created by the User on top of the Software (such as project code, workspaces, scripts, documents, Wiki cards, Spore App and plugin code) is owned by the User or its rights holders. However, intellectual property inherent to the Software itself that is used in the course of creating such content remains owned by the Rights Holder.

3.3 [TBD] If prompts, feedback, or suggestions provided by the User in the Software are adopted by the Rights Holder to improve the Software, the ownership and licensing arrangements arising therefrom must be determined before finalization.

---

## 4. User Data and Privacy

4.1 **Local storage**: The User's projects, workspaces, settings, Wiki knowledge, and conversation records are stored **locally by default** (local file system / local persistent storage). The User retains full control and disposal authority over their own data.

4.2 **API keys belong to the User**: The User configures and keeps their own API keys for large language models or third-party services. Such keys are the User's own credentials, stored locally and used solely under the User's control; the Rights Holder does not collect, store copies of, or use such keys for any purpose other than the User-configured services.

4.3 **No telemetry collection**: The Software does **not collect telemetry data by default** (including but not limited to usage statistics, behavioral analytics, or crash reports). [TBD] If any form of telemetry or anonymous analytics is introduced in the future, the User's explicit consent must be obtained separately before activation, and the data scope, purposes, and opt-out mechanisms must be specified in this Agreement.

4.4 The Rights Holder has no custodial obligation with respect to locally stored data produced by the User's use of the Software; the User is responsible for making their own backups.

---

## 5. Third-Party Component Notices

5.1 The Software incorporates certain Third-Party Components. Such components are provided to the User under their respective license terms; this Agreement does not affect or supersede the rights and obligations set out in those third-party licenses.

5.2 The complete list of Third-Party Components is documented in the `docs/licenses/` directory shipped with the distribution package (including, but not limited to, license reports for Go dependencies, Web frontend dependencies, and packaged assets such as fonts and icons). Users may also view the relevant copyright notices and license summaries in the "About" page of the Software.

5.3 To the extent a Third-Party Component's license conflicts with this Agreement, the third-party license prevails with respect to that component.

---

## 6. Disclaimer of Warranties

6.1 The Software is provided **"AS IS"**, without warranties of any kind, express or implied. The Rights Holder and its licensors make no warranties regarding merchantability, fitness for a particular purpose, non-infringement, absence of errors or defects, or that the results of the Software will meet any particular expectation of the User.

6.2 The Software relies on third-party services such as large language models, whose output may be inaccurate, incomplete, or biased. The User is solely responsible for reviewing, verifying, and bearing the consequences of using content generated by the Software; generated content does not constitute professional advice (including but not limited to legal, medical, or financial advice).

6.3 The User is responsible for the way they use the Software to process their own data (including sensitive data) and must ensure such use complies with applicable laws and regulations.

---

## 7. Limitation of Liability

7.1 To the maximum extent permitted by applicable law, the Rights Holder and its licensors, affiliates, employees, and agents shall not be liable for any **indirect, incidental, special, punitive, or consequential damages** arising out of or in connection with the use of, or the inability to use, the Software (including but not limited to loss of profits, data loss, business interruption, and loss of goodwill).

7.2 [TBD] The **aggregate liability cap** of the Rights Holder under this Agreement (e.g., limited to fees paid, or another fixed cap to be agreed) must be determined before finalization.

7.3 Nothing in this Section excludes or limits liability where, and to the extent that, applicable law does not permit such exclusion or limitation (for example, liability arising from intentional misconduct or gross negligence).

---

## 8. Termination

8.1 This license takes effect when the User begins using the Software and continues until terminated in accordance with this Section.

8.2 If the User breaches any provision of this Agreement, this license (and all other rights granted under this Agreement) **terminates automatically**, without further notice.

8.3 The User may terminate the license granted under this Agreement at any time by ceasing use and uninstalling and deleting all copies of the Software; upon termination, the User must cease all use and delete or destroy all copies and components of the Software.

8.4 Sections 3 (Intellectual Property Ownership), 4 (User Data and Privacy), 6 (Disclaimer of Warranties), 7 (Limitation of Liability), and 9 (Governing Law and Dispute Resolution) survive any termination of this Agreement.

---

## 9. Governing Law and Dispute Resolution

9.1 [TBD] **Governing law**: To be determined (expected to be the law of the Rights Holder's jurisdiction; must be decided and stated before finalization).

9.2 [TBD] **Dispute resolution**: To be determined (expected to be "friendly negotiation first, then referral to [TBD] competent court / arbitration institution"; the specific venue and arbitration institution must be decided and stated before finalization).

9.3 If any provision of this Agreement is held invalid or unenforceable, the remaining provisions shall continue in full force and effect.

---

## 10. Changes to This Agreement

10.1 [TBD] The Rights Holder reserves the right to update this Agreement. Updated terms will be published through designated channels (e.g., the next distribution package or the in-app "About" page); whether prior notice or consent is required before changes take effect must be determined before finalization.

---

## 11. Version and Effective Date

11.1 Agreement version: `v0.1-draft` (draft version, for review only).

11.2 Effective date: `TBD (YYYY-MM-DD)`.

11.3 Software versions covered by this Agreement: [TBD] (the applicable software version or version range to be stated at finalization).

---

## 12. Miscellaneous

12.1 This draft is written in Chinese (Simplified); where an English counterpart exists and the two texts conflict, the Chinese text prevails. [TBD: if both languages are to have equal legal effect upon finalization, this clause must be adjusted by legal professionals.]

12.2 Matters not covered by this draft will be supplemented by the Rights Holder before finalization.