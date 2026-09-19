# 对照版本源码

基线为 `23c4dc5`。01、02、03 是分别相对基线的累计补丁，不能依次叠加。04 只包含相对 credit 的初始映射选项。

| 目录／版本 | 应用补丁 |
|---|---|
| db-base-bin | 原基线 |
| db-index-bin | 01-index.patch |
| db-prepare-bin | 02-prepare.patch |
| db-credit-bin | 03-credit.patch |
| db-map-bin、db-final-bin | 03-credit.patch，再 04-map-addition.patch |

各版本使用独立工作目录及二进制目录，构建四个命令均带 `-tags=comet_v3`；先按项目说明准备固定 Comet overlay。测试补丁快照不包含之后补充的全部测试，最终提交中的测试为完整版本；生产代码行为与被测最终版本一致，并在原构建位置核对了四个二进制的逐字节一致性。

实验脚本保留每轮报告并拒绝复用已存在的新库目录。`run_sequence.py` 假定其他版本二进制已经构建，只额外生成映射候选，不负责替换工作树到旧版本。`binaries.json` 是实际测试文件的 SHA-256，包含构建元数据，不要求在不同检出位置得到相同二进制指纹。
