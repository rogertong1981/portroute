# portroute

### portroute 工具提供将内网端口转发到公网的能力，你可以在可访问internet的私有内网运行forward，然后可以在任意可以上网的地方运行proxy，proxy 可以将私有内网中的端口经internet映射到本机端口，于是你可以像访问本地资源一样访问内网端口了

# 1. center 
center 作为中央服务器存在于网络中，用于连接所有的forward / proxy

# 2. forward
forward 用来连接私有网络，起到私有网络端口转发器的作用，forward可以在运行时指定tunnelkey 标识，也可以由center来分配标识

# 3. proxy
proxy 用来将私有网络的端口映射到运行proxy的主机上，proxy需要先指明tunnelKey, center会识别tunnelkey, 将tunnelKey相同的forward / proxy 配对连接，proxy中还可以指明需要连接到私网的哪个端口，比如：在私网中192.168.100.20:3306上运行了mysql服务，那么proxy可以通知forward来进行中转连接，然后映射到本地端口上，proxy连接完成后，可以通过访问proxy所在主机的本地端口，来连接到私网的mysql
