<?php
!defined('EMLOG_ROOT') && exit('access denied!');

/**
 * pixsy 图床插件设置页（仅管理员可见）
 * 展示：plugin_setting_view()；保存：plugin_setting()
 */

// 设置页可能在插件未启用时被访问（emlog 不检查启用状态就 include 设置文件），
// 此时主文件 pixsy.php 未加载，这里用到的 pixsy_* 函数会全部未定义，先防御式加载。
if (!function_exists('pixsy_get_options')) {
    require_once EMLOG_ROOT . '/content/plugins/pixsy/pixsy.php';
}
// 「修复上传接管」按钮用到 pixsy_claim_upload_hook（定义在回调文件）
if (!function_exists('pixsy_claim_upload_hook')) {
    require_once EMLOG_ROOT . '/content/plugins/pixsy/pixsy_callback.php';
}

function plugin_setting_view()
{
    $storage = Storage::getInstance('pixsy');
    $server = htmlspecialchars(trim((string)$storage->getValue('server')), ENT_QUOTES);
    $username = htmlspecialchars(trim((string)$storage->getValue('username')), ENT_QUOTES);
    $tokenExpire = (int)$storage->getValue('token_expire');
    $hasToken = (string)$storage->getValue('token') !== '' && $tokenExpire > time();
    ?>
    <div class="container-fluid">
        <h3>pixsy 图床设置</h3>
        <p class="text-muted small">
            接管资源上传：后台「资源」上传的图片将直接上传到 pixsy 图床，文章使用 pixsy 返回的外链。
            站点地址形如 <code>https://img.example.com</code>，不带末尾斜杠。
        </p>
        <?php if ($hasToken): ?>
            <div class="alert alert-success py-2 small">pixsy 认证有效，token 至 <?= htmlspecialchars(date('Y-m-d H:i', $tokenExpire)) ?> 前有效。</div>
        <?php else: ?>
            <div class="alert alert-secondary py-2 small">尚未认证或 token 已过期，保存后首次上传时会自动登录 pixsy。</div>
        <?php endif; ?>
        <form action="./plugin.php?plugin=pixsy&amp;action=setting" method="post" class="form" style="max-width:560px">
            <div class="form-group">
                <label>pixsy 站点地址</label>
                <input type="url" name="server" class="form-control" value="<?= $server ?>"
                       placeholder="https://img.example.com" required>
            </div>
            <div class="form-group">
                <label>用户名</label>
                <input type="text" name="username" class="form-control" value="<?= $username ?>" required>
            </div>
            <div class="form-group">
                <label>密码</label>
                <input type="password" name="password" class="form-control" value="" autocomplete="new-password"
                       placeholder="<?= $username !== '' ? '留空则保持原密码不变' : '请输入 pixsy 登录密码' ?>">
            </div>
            <div class="form-group">
                <button type="submit" class="btn btn-sm btn-primary">保存设置</button>
                <button type="submit" name="do" value="test" class="btn btn-sm btn-outline-secondary" formnovalidate>测试连接</button>
                <button type="submit" name="do" value="claim" class="btn btn-sm btn-outline-warning">修复上传接管</button>
            </div>
        </form>
        <?php pixsy_render_diagnostics(); ?>
    </div>
<?php
}

/**
 * 诊断区块：环境检查 + 最近日志，上传失败时先看这里
 */
function pixsy_render_diagnostics()
{
    echo '<h4 class="mt-4">诊断</h4>';
    echo '<ul class="small text-muted">';
    echo '<li>curl 扩展：' . (extension_loaded('curl') ? '<span class="text-success">已加载</span>' : '<span class="text-danger">未加载（无法上传，需安装 php-curl）</span>') . '</li>';
    // upload_media 是单次接管式钩子（只执行第一个注册的函数），显示注册队列与排位
    global $emHooks;
    $queue = isset($emHooks['upload_media']) ? $emHooks['upload_media'] : [];
    if ($queue) {
        $pos = array_search('pixsy_upload_media', $queue, true);
        echo '<li>upload_media 钩子队列：' . htmlspecialchars(implode(' → ', $queue)) . '</li>';
        if ($pos === 0) {
            echo '<li><span class="text-success">pixsy 排在第 1 位，上传由 pixsy 接管。</span></li>';
        } else {
            echo '<li><span class="text-danger">pixsy 排在第 ' . ($pos + 1) . ' 位——上传被排在前面的插件（"' . htmlspecialchars((string)$queue[0]) . '"）接管，pixsy 不会被调用！</span>'
                . '解决办法：点上方「关闭所有插件」，先启用 pixsy，再启用其它插件。</li>';
        }
    } else {
        echo '<li><span class="text-danger">upload_media 钩子队列为空——pixsy 未成功注册（插件文件可能未被加载）</span></li>';
    }
    // 日志文件位置（与 pixsy_log 的落盘候选顺序一致）
    $candidates = [
        EMLOG_ROOT . '/content/plugins/pixsy/pixsy.log',
        EMLOG_ROOT . '/content/cache/pixsy.log',
    ];
    $logFile = '';
    foreach ($candidates as $f) {
        if (file_exists($f)) {
            $logFile = $f;
            break;
        }
    }
    $tail = [];
    if ($logFile !== '') {
        $lines = file($logFile, FILE_IGNORE_NEW_LINES);
        $tail = array_slice((array)$lines, -30);
        echo '<li>日志文件：<code>' . htmlspecialchars($logFile) . '</code>（显示最近 ' . count($tail) . ' 行）</li>';
    } else {
        echo '<li>暂无日志文件（尚未触发上传，或目录均不可写）</li>';
    }
    echo '</ul>';
    if ($tail) {
        echo '<pre style="max-height:260px;overflow:auto;background:#f8f9fa;padding:8px;font-size:12px">' . htmlspecialchars(implode("\n", $tail)) . '</pre>';
    }
}

function plugin_setting()
{
    // do 动作由同表单的提交按钮以 POST 带入（GET 链接会被 emlog 以"无 POST 数据"直接重定向到 error flash）
    $do = isset($_POST['do']) ? (string)$_POST['do'] : '';
    if ($do === 'test') {
        pixsy_test_connection();
        return true;
    }
    if ($do === 'claim') {
        // 手动触发：把 pixsy 排到 upload_media 钩子队列首位
        pixsy_claim_upload_hook();
        emMsg('已把 pixsy 排到上传钩子队列首位，请重新上传测试', './plugin.php?plugin=pixsy');
    }

    $server = isset($_POST['server']) ? trim((string)$_POST['server']) : '';
    $username = isset($_POST['username']) ? trim((string)$_POST['username']) : '';
    $password = isset($_POST['password']) ? (string)$_POST['password'] : '';

    if ($server === '' || $username === '') {
        emMsg('站点地址与用户名不能为空');
    }
    if (!preg_match('#^https?://#i', $server)) {
        emMsg('站点地址必须以 http:// 或 https:// 开头');
    }
    $server = rtrim($server, '/');

    $storage = Storage::getInstance('pixsy');
    $storage->setValue('server', $server, 'string');
    $storage->setValue('username', $username, 'string');
    // 密码留空表示沿用已保存的密码
    if ($password !== '') {
        $storage->setValue('password', $password, 'string');
    }
    // 账号或地址变化后清掉缓存的 token，下次上传重新登录
    $storage->deleteName('token');
    $storage->deleteName('token_expire');

    // 保存后立即验证并缓存 token，尽早暴露配置错误
    $opt = pixsy_get_options();
    $token = pixsy_login($opt['server'], $opt['username'], $opt['password']);
    if ($token === '') {
        emMsg('设置已保存，但 pixsy 登录失败，请检查账号与密码');
    }
    $storage->setValue('token', $token, 'string');
    $storage->setValue('token_expire', time() + 7 * 86400, 'number');
    return true;
}

function pixsy_test_connection()
{
    $opt = pixsy_get_options();
    if (empty($opt['server']) || empty($opt['username']) || empty($opt['password'])) {
        emMsg('请先完整填写并保存设置');
    }
    $token = pixsy_get_token($opt);
    if ($token === '') {
        emMsg('连接失败：无法登录 pixsy，请检查配置');
    }
    emMsg('连接成功，pixsy 认证有效', './plugin.php?plugin=pixsy');
}
