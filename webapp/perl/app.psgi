use v5.38;

use File::Basename;
use Plack::Builder;
use Isupipe::App;

my $root_dir = File::Basename::dirname(__FILE__);

my $app = Isupipe::App->psgi($root_dir);

builder {
    enable 'ReverseProxy';
    enable 'Session::Cookie',
        session_key => 'isupipe_perl',
        domain      => 't.isucon.pw',
        path        => '/',
        expires     => 3600,
        secret      => $ENV{ISUCON13_SESSION_SECRETKEY} || 'defaultsecret';
    $app;
}
