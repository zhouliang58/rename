linux下的使用步骤：

1、修改文件中的目标文件、修改鉴权服务（或者删除该部分代码）

2、交叉编译:GOOS=linux GOARCH=amd64 go build

3、上传可执行文件到服务器，跟目标文件同一个目录

4、让程序后台执行：nohup ./rename &

提供两个http接口：

1、查找是否有某个常量

2、重命名某个常量