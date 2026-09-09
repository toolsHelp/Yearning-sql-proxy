# 第三方组件许可证声明

本项目（Yearning-sql-proxy，程序名 sql-relay）自身采用 [MIT](LICENSE) 许可证。

本文件汇总本项目构建产物所链接的全部第三方 Go 模块的版权与许可证信息，
依据各上游许可证的署名要求随源码与二进制分发一并保留。
许可证全文见文末附录，各组件对应的许可证文本见「许可证」列标注。

分发二进制时，请随附本文件（或等价的许可证声明）。

## 组件清单

### 直接依赖

| 模块 | 版本 | 许可证 | 版权声明 |
|---|---|---|---|
| github.com/go-mysql-org/go-mysql | v1.9.1 | MIT（附录 A） | Copyright (c) 2014 siddontang |
| github.com/gorilla/websocket | v1.5.3 | BSD-2-Clause（附录 B） | Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved. |
| github.com/vmihailenco/msgpack/v5 | v5.4.1 | BSD-2-Clause（附录 B） | Copyright (c) 2013 The github.com/vmihailenco/msgpack Authors. All rights reserved. |
| gopkg.in/yaml.v3 | v3.0.1 | MIT **与** Apache-2.0 双许可（附录 A、D） | Copyright (c) 2006-2011 Kirill Simonov；Copyright (c) 2011-2019 Canonical Ltd |

### 间接依赖（随构建链进入二进制）

| 模块 | 版本 | 许可证 | 版权声明 |
|---|---|---|---|
| github.com/Masterminds/semver | v1.5.0 | MIT（附录 A） | Copyright (C) 2014-2019, Matt Butcher and Matt Farina |
| github.com/goccy/go-json | v0.10.2 | MIT（附录 A） | Copyright (c) 2020 Masaaki Goshima |
| github.com/google/uuid | v1.3.0 | BSD-3-Clause（附录 C） | Copyright (c) 2009,2014 Google Inc. All rights reserved. |
| github.com/klauspost/compress | v1.17.8 | BSD-3-Clause（附录 C） | Copyright (c) 2012 The Go Authors. All rights reserved.；Copyright (c) 2019 Klaus Post. All rights reserved. |
| github.com/pingcap/errors | v0.11.5-0.20221009092201-b66cddb77c32 | BSD-3-Clause（附录 C） | Copyright (c) 2015, Dave Cheney <dave@cheney.net> All rights reserved. |
| github.com/pingcap/log | v1.1.1-0.20230317032135-a0d097d16e22 | Apache-2.0（附录 D） | PingCAP, Inc.（上游 LICENSE 为 Apache-2.0 标准文本） |
| github.com/pingcap/tidb/pkg/parser | v0.0.0-20231103042308-035ad5ccbe67 | Apache-2.0（附录 D） | PingCAP, Inc.（上游 LICENSE 为 Apache-2.0 标准文本） |
| github.com/shopspring/decimal | v1.2.0 | MIT（附录 A） | Copyright (c) 2015 Spring, Inc. |
| github.com/siddontang/go | v0.0.0-20180604090527-bdc77568d726 | MIT（附录 A） | Copyright (c) 2014 siddontang |
| github.com/siddontang/go-log | v0.0.0-20180807004314-8d05993dda07 | MIT（附录 A） | Copyright (c) 2014 siddontang |
| github.com/vmihailenco/tagparser/v2 | v2.0.0 | BSD-2-Clause（附录 B） | Copyright (c) 2019 The github.com/vmihailenco/tagparser Authors. All rights reserved. |
| go.uber.org/atomic | v1.11.0 | MIT（附录 A） | Copyright (c) 2016 Uber Technologies, Inc. |
| go.uber.org/multierr | v1.11.0 | MIT（附录 A） | Copyright (c) 2017-2021 Uber Technologies, Inc. |
| go.uber.org/zap | v1.26.0 | MIT（附录 A） | Copyright (c) 2016-2017 Uber Technologies, Inc. |
| golang.org/x/exp | v0.0.0-20231006140011-7918f672742d | BSD-3-Clause（附录 C） | Copyright (c) 2009 The Go Authors. All rights reserved. |
| golang.org/x/text | v0.13.0 | BSD-3-Clause（附录 C） | Copyright (c) 2009 The Go Authors. All rights reserved. |
| gopkg.in/natefinch/lumberjack.v2 | v2.2.1 | MIT（附录 A） | Copyright (c) 2014 Nate Finch |

上述组件均为宽松许可证（permissive license），无 GPL / LGPL / AGPL 组件。

## 未进入构建产物的依赖

以下模块仅出现在 `go.sum` 中，是上游模块的测试依赖，不参与本项目构建、
不链接进分发二进制，因此其许可证义务不适用于本项目的分发：

| 模块 | 版本 | 许可证 | 说明 |
|---|---|---|---|
| github.com/go-sql-driver/mysql | v1.7.1 | MPL-2.0（弱 copyleft） | 仅 go-mysql 的测试依赖 |
| github.com/stretchr/testify | v1.8.4 | MIT | 仅测试断言库 |

## gopkg.in/yaml.v3 的 NOTICE 声明

以下为上游 `gopkg.in/yaml.v3 v3.0.1` 的 NOTICE 文件原文，
依据 Apache License 2.0 第 4(d) 条要求原样保留。

```
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

## 许可证全文附录

以下附录为各上游模块 LICENSE 文件的原文（节选自对应版本的模块包）。

### 附录 A — MIT License

来源：github.com/go-mysql-org/go-mysql v1.9.1 的 LICENSE 文件原文。同时适用于 goccy/go-json、Masterminds/semver、shopspring/decimal、siddontang/go、siddontang/go-log、go.uber.org/{atomic,multierr,zap}、natefinch/lumberjack.v2，以及 yaml.v3 的 MIT 部分。

```text
The MIT License (MIT)

Copyright (c) 2014 siddontang

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

```

### 附录 B — BSD 2-Clause License

来源：github.com/gorilla/websocket v1.5.3 的 LICENSE 文件原文。同时适用于 vmihailenco/msgpack/v5、vmihailenco/tagparser/v2。

```text
Copyright (c) 2013 The Gorilla WebSocket Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

  Redistributions of source code must retain the above copyright notice, this
  list of conditions and the following disclaimer.

  Redistributions in binary form must reproduce the above copyright notice,
  this list of conditions and the following disclaimer in the documentation
  and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

```

### 附录 C — BSD 3-Clause License

来源：github.com/google/uuid v1.3.0 的 LICENSE 文件原文。同时适用于 klauspost/compress、pingcap/errors、golang.org/x/exp、golang.org/x/text。

```text
Copyright (c) 2009,2014 Google Inc. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

```

### 附录 D — Apache License 2.0

来源：github.com/pingcap/log v1.1.1-0.20230317032135-a0d097d16e22 的 LICENSE 文件原文。同时适用于 pingcap/tidb/pkg/parser，以及 yaml.v3 的 Apache 部分。

```text
                                 Apache License
                           Version 2.0, January 2004
                        http://www.apache.org/licenses/

   TERMS AND CONDITIONS FOR USE, REPRODUCTION, AND DISTRIBUTION

   1. Definitions.

      "License" shall mean the terms and conditions for use, reproduction,
      and distribution as defined by Sections 1 through 9 of this document.

      "Licensor" shall mean the copyright owner or entity authorized by
      the copyright owner that is granting the License.

      "Legal Entity" shall mean the union of the acting entity and all
      other entities that control, are controlled by, or are under common
      control with that entity. For the purposes of this definition,
      "control" means (i) the power, direct or indirect, to cause the
      direction or management of such entity, whether by contract or
      otherwise, or (ii) ownership of fifty percent (50%) or more of the
      outstanding shares, or (iii) beneficial ownership of such entity.

      "You" (or "Your") shall mean an individual or Legal Entity
      exercising permissions granted by this License.

      "Source" form shall mean the preferred form for making modifications,
      including but not limited to software source code, documentation
      source, and configuration files.

      "Object" form shall mean any form resulting from mechanical
      transformation or translation of a Source form, including but
      not limited to compiled object code, generated documentation,
      and conversions to other media types.

      "Work" shall mean the work of authorship, whether in Source or
      Object form, made available under the License, as indicated by a
      copyright notice that is included in or attached to the work
      (an example is provided in the Appendix below).

      "Derivative Works" shall mean any work, whether in Source or Object
      form, that is based on (or derived from) the Work and for which the
      editorial revisions, annotations, elaborations, or other modifications
      represent, as a whole, an original work of authorship. For the purposes
      of this License, Derivative Works shall not include works that remain
      separable from, or merely link (or bind by name) to the interfaces of,
      the Work and Derivative Works thereof.

      "Contribution" shall mean any work of authorship, including
      the original version of the Work and any modifications or additions
      to that Work or Derivative Works thereof, that is intentionally
      submitted to Licensor for inclusion in the Work by the copyright owner
      or by an individual or Legal Entity authorized to submit on behalf of
      the copyright owner. For the purposes of this definition, "submitted"
      means any form of electronic, verbal, or written communication sent
      to the Licensor or its representatives, including but not limited to
      communication on electronic mailing lists, source code control systems,
      and issue tracking systems that are managed by, or on behalf of, the
      Licensor for the purpose of discussing and improving the Work, but
      excluding communication that is conspicuously marked or otherwise
      designated in writing by the copyright owner as "Not a Contribution."

      "Contributor" shall mean Licensor and any individual or Legal Entity
      on behalf of whom a Contribution has been received by Licensor and
      subsequently incorporated within the Work.

   2. Grant of Copyright License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      copyright license to reproduce, prepare Derivative Works of,
      publicly display, publicly perform, sublicense, and distribute the
      Work and such Derivative Works in Source or Object form.

   3. Grant of Patent License. Subject to the terms and conditions of
      this License, each Contributor hereby grants to You a perpetual,
      worldwide, non-exclusive, no-charge, royalty-free, irrevocable
      (except as stated in this section) patent license to make, have made,
      use, offer to sell, sell, import, and otherwise transfer the Work,
      where such license applies only to those patent claims licensable
      by such Contributor that are necessarily infringed by their
      Contribution(s) alone or by combination of their Contribution(s)
      with the Work to which such Contribution(s) was submitted. If You
      institute patent litigation against any entity (including a
      cross-claim or counterclaim in a lawsuit) alleging that the Work
      or a Contribution incorporated within the Work constitutes direct
      or contributory patent infringement, then any patent licenses
      granted to You under this License for that Work shall terminate
      as of the date such litigation is filed.

   4. Redistribution. You may reproduce and distribute copies of the
      Work or Derivative Works thereof in any medium, with or without
      modifications, and in Source or Object form, provided that You
      meet the following conditions:

      (a) You must give any other recipients of the Work or
          Derivative Works a copy of this License; and

      (b) You must cause any modified files to carry prominent notices
          stating that You changed the files; and

      (c) You must retain, in the Source form of any Derivative Works
          that You distribute, all copyright, patent, trademark, and
          attribution notices from the Source form of the Work,
          excluding those notices that do not pertain to any part of
          the Derivative Works; and

      (d) If the Work includes a "NOTICE" text file as part of its
          distribution, then any Derivative Works that You distribute must
          include a readable copy of the attribution notices contained
          within such NOTICE file, excluding those notices that do not
          pertain to any part of the Derivative Works, in at least one
          of the following places: within a NOTICE text file distributed
          as part of the Derivative Works; within the Source form or
          documentation, if provided along with the Derivative Works; or,
          within a display generated by the Derivative Works, if and
          wherever such third-party notices normally appear. The contents
          of the NOTICE file are for informational purposes only and
          do not modify the License. You may add Your own attribution
          notices within Derivative Works that You distribute, alongside
          or as an addendum to the NOTICE text from the Work, provided
          that such additional attribution notices cannot be construed
          as modifying the License.

      You may add Your own copyright statement to Your modifications and
      may provide additional or different license terms and conditions
      for use, reproduction, or distribution of Your modifications, or
      for any such Derivative Works as a whole, provided Your use,
      reproduction, and distribution of the Work otherwise complies with
      the conditions stated in this License.

   5. Submission of Contributions. Unless You explicitly state otherwise,
      any Contribution intentionally submitted for inclusion in the Work
      by You to the Licensor shall be under the terms and conditions of
      this License, without any additional terms or conditions.
      Notwithstanding the above, nothing herein shall supersede or modify
      the terms of any separate license agreement you may have executed
      with Licensor regarding such Contributions.

   6. Trademarks. This License does not grant permission to use the trade
      names, trademarks, service marks, or product names of the Licensor,
      except as required for reasonable and customary use in describing the
      origin of the Work and reproducing the content of the NOTICE file.

   7. Disclaimer of Warranty. Unless required by applicable law or
      agreed to in writing, Licensor provides the Work (and each
      Contributor provides its Contributions) on an "AS IS" BASIS,
      WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
      implied, including, without limitation, any warranties or conditions
      of TITLE, NON-INFRINGEMENT, MERCHANTABILITY, or FITNESS FOR A
      PARTICULAR PURPOSE. You are solely responsible for determining the
      appropriateness of using or redistributing the Work and assume any
      risks associated with Your exercise of permissions under this License.

   8. Limitation of Liability. In no event and under no legal theory,
      whether in tort (including negligence), contract, or otherwise,
      unless required by applicable law (such as deliberate and grossly
      negligent acts) or agreed to in writing, shall any Contributor be
      liable to You for damages, including any direct, indirect, special,
      incidental, or consequential damages of any character arising as a
      result of this License or out of the use or inability to use the
      Work (including but not limited to damages for loss of goodwill,
      work stoppage, computer failure or malfunction, or any and all
      other commercial damages or losses), even if such Contributor
      has been advised of the possibility of such damages.

   9. Accepting Warranty or Additional Liability. While redistributing
      the Work or Derivative Works thereof, You may choose to offer,
      and charge a fee for, acceptance of support, warranty, indemnity,
      or other liability obligations and/or rights consistent with this
      License. However, in accepting such obligations, You may act only
      on Your own behalf and on Your sole responsibility, not on behalf
      of any other Contributor, and only if You agree to indemnify,
      defend, and hold each Contributor harmless for any liability
      incurred by, or claims asserted against, such Contributor by reason
      of your accepting any such warranty or additional liability.

   END OF TERMS AND CONDITIONS

   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. We also recommend that a
      file or class name and description of purpose be included on the
      same "printed page" as the copyright notice for easier
      identification within third-party archives.

   Copyright [yyyy] [name of copyright owner]

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.

```

