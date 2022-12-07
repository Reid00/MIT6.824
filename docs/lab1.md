# MapReduce

## 原理
Map, Reduce 是大数据入门的经典思想，运用分治思想。

Map 端，将数据分为若干个切片(这个切片是个逻辑概念), 让Work 对不同的split 进行操作。
读取文件内容，进行mapF 函数，生成临时中间文件。等到所有的Work 结束后，启动Reduce 阶段。
读取所需(map 阶段的Key hash mod 得到reduce id)的临时文件，进行reduceF 函数。
需要注意(优化)的是:
1. mapF 的时候，需要在Map 端 根据分区器(key hash 然后mod)确定好reduce id, 然后生成在内存中需要flush
到硬盘时，根据P(分区id), K(Map端的key), V(Map端的Value) 进行排序好之后，保持分区间有序和分区内有序，效率会更好

2. Reduce 用归并算法读取若干个临时中间文件

![流程图](https://cdn.staticaly.com/gh/Reid00/image-host@main/20221207/image.2kn39l9vezu0.webp)