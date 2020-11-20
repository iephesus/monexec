### Monexec Config配置说明
- #### 格式 
  - 配置文件必须为 **.yml** 或 **.yaml** 结尾
  - 加载方式为monexec start ~/配置文件位置
  - **services**下每一个 **label** 及其后的参数即为一个服务
  - **除services外**的其他项即为插件
- #### 服务
  - ##### 完整例子: 
  - ``` yaml
    services:
    - label: Demo1
      command: /bin/bash
      args:
        - -c
        - nc -l 9000
      stop_timeout: 5s
      restart_delay: 5s
      restart: -1
      logFile: "/var/log/demo1.log"
      workdir: "/home/monexec"
      environment:
        SOME_PARAM: some value
        ANOTHER_PARAM: another value
        THIRD_PARAM: hello
        FOUR_PARAM: world
    ```
  - ##### 精简格式:
  - ``` yaml
    - label: Demo2
      command: ls
      args:
    ```
- #### 插件
   - ##### 必须配置的插件为assist
      - ``` yaml
        assist:
            machine: DemoMachine1
            ip: "192.168.1.1"
            hotReload: true
            users:
               - {username: "demouser1", password: "123456789"}
               - {username: "demouser2", password: "11223344"}
        ```
     - **machine**指定Web UI页面中显示的机器名
     - **ip**指定Web UI页面中显示的机器IP地址
     - **hotReload**设定监控程序是否启用配置文件热重载，当启用的时候目前可自动加载 **新增** 的服务service和插件plugin，且新增的服务中**command**和**args**参数必须与已有的服务不同
     - **users**为配置Web UI中登录的用户名与密码，可配置多对且登录时使用任一帐号即可
   - ##### 如果要启用Web UI的话需要配置rest插件
     - ``` yaml
       rest:
         listen: "0.0.0.0:9980"
         cors: true
       ```
      - listen指定Web UI 的访问地址
      - cors设置是否启用跨域资源共享
