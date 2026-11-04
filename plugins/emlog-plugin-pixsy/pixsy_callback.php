<?php
!defined('EMLOG_ROOT') && exit('access denied!');

/**
 * pixsy 图床插件启用/卸载回调
 * emlog 约定：本文件仅在插件启用与卸载时被加载一次，
 * callback_init/callback_rm 是全局函数名，必须放这里而不是主文件。
 */

function pixsy_callback_init_storage()
{
    // 首次启用时初始化设置默认值
    $storage = Storage::getInstance('pixsy');
    if (!$storage->checkNameExist('keep_local')) {
        $storage->setValue('keep_local', 'n', 'string');
    }
}

function callback_init()
{
    pixsy_callback_init_storage();
    pixsy_claim_upload_hook();
}

/**
 * 把 pixsy 排到 active_plugins 首位。
 * upload_media 是单次接管式钩子（doOnceAction 只执行第一个注册的函数），
 * 注册顺序 = active_plugins 顺序，若其它上传类插件先启用，pixsy 将永远不被调用。
 * 启用本插件时自动重排，避免依赖用户手工调整启用顺序。
 */
function pixsy_claim_upload_hook()
{
    $active = Option::get('active_plugins');
    if (!is_array($active)) {
        $active = [];
    }
    $me = 'pixsy/pixsy.php';
    $pos = array_search($me, $active, true);
    if ($pos === 0) {
        return true; // 已在首位
    }
    // 注意：callback_init 在 activePlugin 写库之后、缓存刷新之前执行，
    // Option::get 读到的可能是旧缓存（pixsy 还不在里面），此时也要插入。
    if ($pos !== false) {
        unset($active[$pos]);
    }
    array_unshift($active, $me);
    Option::updateOption('active_plugins', serialize(array_values($active)));
    // 立即生效：刷新 options 缓存
    $CACHE = Cache::getInstance();
    $CACHE->updateCache('options');
    return true;
}

function callback_rm()
{
    // 卸载插件时清空插件数据（token、id 映射）
    Storage::getInstance('pixsy')->deleteAllName('YES');
}
